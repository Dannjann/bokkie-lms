//go:build windows

package main

import (
	"errors"
	"os"

	"github.com/Dannjann/bokkie-lms/internal/app"
	"github.com/Dannjann/bokkie-lms/internal/config"
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

	st := newStation(dataDir, opts, !detached, stationPlatform{
		openURL:  winsetup.OpenInBrowser,
		message:  winsetup.ShowMessage,
		firewall: winsetup.CheckFirewall,
		startFailed: func(err error) {
			if folder, ok := config.UnwritableFolder(err); ok {
				offerFolderFix(folder)
			}
		},
		addrInUse: isAddrInUse,
	})
	go st.start()
	return winsetup.RunStationWindow(st.hooks())
}

// offerFolderFix is the one start-up failure with a remedy this window can
// apply: a ledger left behind by an older Bokkie in a folder this account may
// not write. Granting it needs administrator rights, so it goes through the
// same elevated step the setup window has always used.
func offerFolderFix(folder string) {
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

func isAddrInUse(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE) || errors.Is(err, windows.WSAEACCES)
}

// openedAsApp is a double-click: no arguments, and a console Windows made for
// this process alone.
func openedAsApp() bool {
	return winsetup.LaunchedFromExplorer(winsetup.ConsoleProcessCount(), len(os.Args))
}
