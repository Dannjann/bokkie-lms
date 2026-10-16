//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/app"
	"github.com/Dannjann/bokkie-lms/internal/buildinfo"
	"github.com/Dannjann/bokkie-lms/internal/config"
	"github.com/Dannjann/bokkie-lms/internal/diagnose"
	"github.com/Dannjann/bokkie-lms/internal/winsetup"
	"golang.org/x/sys/windows"
)

// runStationWindow is the Bokkie a person opens from the Start menu: a native
// window that shows whether the station is serving, and that is the station's
// switch. The server runs exactly as long as the window is open.
//
// This replaces the browser window as the switch. A browser launched with
// --app can hand its window to another browser process and exit at once; the
// station, waiting on that process, then shut itself down a second after it
// started, and the window the person was looking at said "refused to connect".
// A window this process owns cannot do that.
func runStationWindow(dataDir string, opts app.Options) error {
	if winsetup.FocusRunningStation() {
		return nil
	}

	// One process on this console means Windows made the console for us — a
	// Start-menu shortcut or a double-click. It is not wanted: the window is
	// the program. Typed at a prompt, the console is somebody's terminal and
	// the log goes on being printed to it.
	detached := winsetup.ConsoleProcessCount() <= 1
	if detached {
		winsetup.HideConsole()
	}

	st := &station{
		dataDir:    dataDir,
		opts:       opts,
		console:    !detached,
		started:    make(chan struct{}),
		stopWanted: make(chan struct{}),
		facts: winsetup.StationFacts{
			State:   winsetup.StationStarting,
			Version: buildinfo.Version,
			Port:    defaultPort,
		},
	}
	go st.start()

	return winsetup.RunStationWindow(winsetup.StationHooks{
		Title:      "Bokkie",
		Snapshot:   st.view,
		Open:       st.open,
		Check:      st.check,
		Stop:       st.stop,
		StopWanted: st.stopWanted,
	})
}

// station is the running server as the window sees it.
type station struct {
	dataDir string
	opts    app.Options
	console bool

	// started closes when start has finished, whether or not it succeeded,
	// so stop can wait for a start still in progress rather than racing it.
	started    chan struct{}
	stopWanted chan struct{}
	done       chan struct{}

	mu    sync.Mutex
	facts winsetup.StationFacts

	cfg      config.Config
	log      *slog.Logger
	closeLog io.Closer
	server   *app.App
}

func (s *station) start() {
	defer close(s.started)

	cfg, log, closeLog, err := bootstrap(s.dataDir, s.console)
	if err != nil {
		s.fail(err)
		if folder, ok := config.UnwritableFolder(err); ok {
			go s.offerFolderFix(folder)
		}
		return
	}
	s.cfg, s.log, s.closeLog = cfg, log, closeLog
	s.update(func(f *winsetup.StationFacts) {
		f.Port, f.DataDir = cfg.Port, cfg.DataDir
		if cfg.MDNS.Enabled {
			f.Hostname = cfg.MDNS.Hostname
		}
	})

	server, err := app.New(context.Background(), cfg, log, s.opts)
	if err != nil {
		log.Error("could not open the station", "error", err)
		s.fail(err)
		return
	}
	if err := server.Start(); err != nil {
		log.Error("could not start serving", "error", err)
		s.fail(portHint(err, cfg.Port))
		return
	}
	s.server = server
	s.update(func(f *winsetup.StationFacts) { f.State = winsetup.StationRunning })
	log.Info("the station is open in its window; closing the window stops it", "port", cfg.Port)

	// The web screen's Stop button is the other way to close the window.
	go func() {
		<-server.StopWanted()
		close(s.stopWanted)
	}()

	s.done = make(chan struct{})
	go s.watch()
}

// watch keeps the facts the window draws current. It asks the station's own
// health check over the network rather than trusting that a bound port is a
// serving one, because "the port is open but the address refuses" is exactly
// the fault this window has to be able to show.
func (s *station) watch() {
	client := &http.Client{Timeout: 3 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", s.cfg.Port)
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()

	// The firewall is asked less often: it is two child processes, and a
	// network profile changes when somebody changes it, not every second.
	var blocked *winsetup.Blocked
	var lastFirewall time.Time

	for {
		if time.Since(lastFirewall) > 30*time.Second {
			lastFirewall = time.Now()
			if b, isBlocked, ok := winsetup.CheckFirewall(); ok {
				blocked = nil
				if isBlocked {
					blocked = &b
					s.log.Warn("Windows is keeping the other computers out", "problem", b.Problem, "fix", b.Fix)
				}
			}
		}

		answering := false
		if resp, err := client.Get(url); err == nil {
			answering = resp.StatusCode == http.StatusOK
			resp.Body.Close()
		} else {
			s.log.Warn("the station's own address did not answer", "address", url, "error", err)
		}
		at, _ := s.server.LastBackup()
		addresses := diagnose.LocalIPv4()

		s.update(func(f *winsetup.StationFacts) {
			f.Asked, f.Answering = true, answering
			f.LastBackup, f.Addresses = at, addresses
			f.Blocked = blocked
		})

		select {
		case <-s.done:
			return
		case <-tick.C:
		}
	}
}

func (s *station) stop() {
	<-s.started
	if s.done != nil {
		close(s.done)
	}
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownWait)
		if err := s.server.Stop(ctx); err != nil {
			s.log.Warn("the station did not stop cleanly", "error", err)
		}
		cancel()
	}
	if s.closeLog != nil {
		s.closeLog.Close()
	}
}

func (s *station) open() {
	s.mu.Lock()
	port := s.facts.Port
	s.mu.Unlock()
	if err := winsetup.OpenInBrowser(winsetup.LocalAddress(port)); err != nil {
		winsetup.ShowMessage("Bokkie", "Could not open a browser on this computer:\n\n"+err.Error(), true)
	}
}

func (s *station) check() string {
	return diagnoseHost(s.dataDir).String()
}

func (s *station) view() winsetup.StationView {
	s.mu.Lock()
	f := s.facts
	s.mu.Unlock()
	f.Now = time.Now()
	return winsetup.DescribeStation(f)
}

func (s *station) update(change func(*winsetup.StationFacts)) {
	s.mu.Lock()
	change(&s.facts)
	s.mu.Unlock()
}

func (s *station) fail(err error) {
	s.update(func(f *winsetup.StationFacts) {
		f.State, f.Err = winsetup.StationFailed, err
	})
}

// offerFolderFix is the one start-up failure with a remedy this window can
// apply: a ledger left behind by an older Bokkie in a folder this account may
// not write. Granting it needs administrator rights, so it goes through the
// same elevated step the setup window has always used.
func (s *station) offerFolderFix(folder string) {
	if !winsetup.AskYesNo("Bokkie",
		"Bokkie found its records in\n    "+folder+"\nbut this account cannot write there.\n\n"+
			"Nothing has been changed and no records have been lost.\n\n"+
			"Let Bokkie fix this now? Windows will ask for permission once.") {
		return
	}
	exe, err := os.Executable()
	if err == nil {
		err = winsetup.Elevate(exe, winsetup.ElevatedArgs(nameOf(actionFixDataFolder)))
	}
	if err != nil {
		winsetup.ShowMessage("Bokkie", err.Error(), !errors.Is(err, winsetup.ErrElevationCancelled))
		return
	}
	winsetup.ShowMessage("Bokkie",
		"When the permission window says it is done, close this window and open Bokkie again.", false)
}

// portHint turns the one start-up failure everybody meets — the port is
// already taken — into a sentence about what to do.
func portHint(err error, port int) error {
	if !isAddrInUse(err) {
		return err
	}
	return fmt.Errorf("port %d is already in use — another Bokkie, or another program, is using it. "+
		"Close the other one and open Bokkie again (%w)", port, err)
}

func isAddrInUse(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE) || errors.Is(err, windows.WSAEACCES)
}
