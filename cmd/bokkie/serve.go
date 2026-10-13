package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/app"
	"github.com/Dannjann/bokkie-lms/internal/config"
	"github.com/Dannjann/bokkie-lms/internal/winsetup"
)

// shutdownWait bounds the drain. It is longer than any request this system
// serves and far shorter than a person's patience with a window that will not
// close.
const shutdownWait = 10 * time.Second

// cmdServe runs the station in this window. Closing the window stops it, and
// nothing else starts it — see docs/plans/02.7-window-is-the-server.md.
func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "override the per-OS data directory")
	noWindow := fs.Bool("no-window", false, "do not open a window; this console is the switch")
	dev := fs.Bool("dev", false, "serve the frontend from the Vite dev server instead of the embedded build")
	viteURL := fs.String("vite-url", defaultViteURL, "address of the Vite dev server, with -dev")
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts, err := runOptions(*dev, *viteURL)
	if err != nil {
		return err
	}

	cfg, log, closeLog, err := bootstrap(*dataDir, true)
	if err != nil {
		// A data folder this user cannot write is not a fault to report to the
		// developer: it is one command for the person standing there. Said as
		// a verdict, the window prints it and nothing else.
		if errors.Is(err, config.ErrDataFolderUnwritable) {
			return winsetup.Verdict(err.Error())
		}
		return err
	}
	defer closeLog.Close()

	server, err := app.New(context.Background(), cfg, log, opts)
	if err != nil {
		return err
	}

	// Start serving before opening the window, so the window has something to
	// load rather than a connection error the person has to refresh past.
	if err := server.Start(); err != nil {
		return err
	}

	var stop <-chan struct{} = make(chan struct{})
	if !*noWindow {
		// The window opens on the station's own screen, not the lending app:
		// the person opening it is switching the station on, and what they
		// need to see first is whether it came up and where to reach it.
		var closeWindow func()
		stop, closeWindow = windowClosed(log, fmt.Sprintf("http://localhost:%d/station", cfg.Port))
		// The station can stop without the window being closed — the Stop
		// button, or Ctrl+C. Leaving it open would show a screen with nothing
		// behind it, and leave a browser profile in the temp folder that
		// nothing ever removes. On a station used daily for years that is a
		// steady leak on the system drive.
		defer closeWindow()
	}

	return waitThenStop(context.Background(), log, server, stop)
}

// serveUntil serves until something says to stop, then shuts down cleanly.
//
// The reason to stop arrives as a channel rather than being discovered here:
// Ctrl+C, a closed console, and the window being closed are three different
// events that must all end in the same drain, and a function that knew about
// any one of them could not be tested without producing it.
func serveUntil(ctx context.Context, log *slog.Logger, server *app.App, stop <-chan struct{}) error {
	if err := server.Start(); err != nil {
		return err
	}
	return waitThenStop(ctx, log, server, stop)
}

// waitThenStop is serveUntil without the start, for the caller that has already
// started the server because it needed an address to open a window on.
func waitThenStop(ctx context.Context, log *slog.Logger, server *app.App, stop <-chan struct{}) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case <-stop:
		log.Info("the window was closed; stopping")
	case <-server.StopWanted():
		log.Info("the Stop button was pressed; stopping")
	case sig := <-signals:
		log.Info("stopping", "signal", sig)
	case <-ctx.Done():
	}

	shutdown, cancel := context.WithTimeout(context.Background(), shutdownWait)
	defer cancel()
	return server.Stop(shutdown)
}

// windowClosed closes when the browser window the station was opened in exits.
//
// That window is the station's switch: while it is open the station is served,
// and closing it stops everything. Nothing else may start it — see D1 in
// docs/plans/02.7-window-is-the-server.md.
func windowClosed(log *slog.Logger, address string) (<-chan struct{}, func()) {
	stop := make(chan struct{})
	nothing := func() {}

	profile, err := os.MkdirTemp("", "bokkie-window-")
	if err != nil {
		log.Warn("could not make a profile directory for the window; this console is the switch now",
			"error", err)
		return stop, nothing
	}

	process, err := winsetup.OpenWindow(address, profile)
	if err != nil {
		// No browser is not a reason to refuse to serve. The console stays the
		// switch, and the person is told which window to close.
		os.RemoveAll(profile)
		log.Warn("could not open a window; this console is the station's switch now",
			"close_this_to_stop", true, "address", address, "error", err)
		return stop, nothing
	}

	log.Info("the station is open in a window; closing it stops the station", "address", address)

	done := make(chan struct{})
	go func() {
		process.Wait()
		close(done)
		os.RemoveAll(profile)
		close(stop)
	}()

	// Closing the other way round: the station stopped first, so the window is
	// taken away rather than left showing a screen with nothing behind it.
	// Waiting for the goroutine is what makes the profile removal happen before
	// the process exits.
	return stop, func() {
		select {
		case <-done:
		default:
			process.Kill()
		}
		<-done
		os.RemoveAll(profile)
	}
}
