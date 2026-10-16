//go:build darwin

package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/Dannjann/bokkie-lms/internal/app"
	"github.com/Dannjann/bokkie-lms/internal/macui"
	"github.com/Dannjann/bokkie-lms/internal/service"
	"github.com/Dannjann/bokkie-lms/internal/winsetup"
)

// AppKit may only be used from the main thread, and the main goroutine is
// only guaranteed to stay on it if it is locked there before main runs.
func init() { runtime.LockOSThread() }

// runStationWindow is Bokkie.app: the same station window as on Windows, drawn
// with AppKit. The server runs exactly as long as the window is open.
func runStationWindow(dataDir string, opts app.Options) error {
	if dataDir == "" {
		dir, err := appDataDir()
		if err != nil {
			return err
		}
		dataDir = dir
	}

	// Started from Finder there is no terminal, and the log file is the
	// record. Typed at one, the log goes on being printed there too.
	console := !launchedAsApp()

	st := newStation(dataDir, opts, console, stationPlatform{
		openURL: macui.OpenInBrowser,
		message: macui.ShowMessage,
		// The macOS application firewall asks for itself the first time the
		// port is opened; there is no rule of ours to check.
		firewall:    func() (winsetup.Blocked, bool, bool) { return winsetup.Blocked{}, false, false },
		startFailed: explainMacStartFailure,
		addrInUse:   func(err error) bool { return errors.Is(err, syscall.EADDRINUSE) },
	})
	go st.start()
	return macui.Run(st.hooks())
}

// appDataDir is ~/Library/Application Support/Bokkie.
//
// Not the system-wide folder the command-line install uses: that belongs to
// the launchd service and its _bokkie account, and the person who opens the
// app cannot write it. The app runs as that person, so its records are theirs.
func appDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "Bokkie"), nil
}

// launchedAsApp is whether this binary is the one inside Bokkie.app.
func launchedAsApp() bool {
	exe, err := os.Executable()
	return err == nil && strings.Contains(exe, ".app/Contents/MacOS/")
}

// explainMacStartFailure names the one cause a Mac has that Windows does not:
// the launchd service from `bokkie install` already holds the port.
func explainMacStartFailure(err error) {
	if !errors.Is(err, syscall.EADDRINUSE) {
		return
	}
	if installed, _ := service.IsInstalled(service.Options{Port: defaultPort}); !installed {
		return
	}
	macui.ShowMessage("Bokkie is already running on this Mac",
		"Bokkie is also installed as a background service here, and that copy is "+
			"already serving the station. Use it instead, or remove it in Terminal with:\n\n"+
			"    sudo /usr/local/bin/bokkie uninstall\n\n"+
			"and open this app again.", false)
}

// openedAsApp is Bokkie.app opened from Finder or the Dock. Older macOS passes
// a -psn_ process serial number as the only argument; nothing else does.
func openedAsApp() bool {
	if !launchedAsApp() {
		return false
	}
	return len(os.Args) < 2 || (len(os.Args) == 2 && strings.HasPrefix(os.Args[1], "-psn_"))
}
