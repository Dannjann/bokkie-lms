package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Dannjann/bokkie-lms/internal/buildinfo"
	"github.com/Dannjann/bokkie-lms/internal/service"
	"github.com/Dannjann/bokkie-lms/internal/winsetup"
)

// setupState is what the guided window knows about this machine before it
// offers anything.
type setupState struct {
	Installed        bool
	InstalledVersion string
	ThisVersion      string
	Elevated         bool

	// RunningFromInstallPath is the person having double-clicked the copy in
	// Program Files rather than the one they downloaded.
	RunningFromInstallPath bool
}

type setupAction int

const (
	actionInstall setupAction = iota
	actionUpdate
	actionRepair
	actionShortcut
	actionUninstall
	actionQuit
)

// setupChoice is one line of the menu.
type setupChoice struct {
	Action     setupAction
	Label      string
	NeedsAdmin bool
}

// adminActions are the choices that change this computer for everyone who uses
// it. The shortcut is deliberately absent: it writes one file to one desktop,
// and a counter PC is where it is usually run, by somebody who does not have an
// administrator password and should not need one.
var adminActions = map[setupAction]bool{
	actionInstall:   true,
	actionUpdate:    true,
	actionRepair:    true,
	actionUninstall: true,
}

// needsAdmin is read both when the menu is built and when a choice is
// dispatched, so the prompt and the label cannot disagree.
func needsAdmin(a setupAction) bool { return adminActions[a] }

// menuFor decides what this machine is offered.
//
// The counter PCs are the reason the shortcut is offered first on a machine
// with nothing installed, and the reason it is marked as needing no
// administrator: most people running this file are standing at a counter PC
// that must never become a second host.
func menuFor(state setupState) []setupChoice {
	choice := func(a setupAction, label string) setupChoice {
		return setupChoice{Action: a, Label: label, NeedsAdmin: needsAdmin(a)}
	}
	shortcut := choice(actionShortcut, "Put a Bokkie shortcut on this computer's desktop")
	closeIt := choice(actionQuit, "Close")

	if !state.Installed {
		return []setupChoice{
			shortcut,
			choice(actionInstall, "Install Bokkie on this computer (this makes it the host)"),
			closeIt,
		}
	}

	var first setupChoice
	switch {
	// An install that is not answering has no version to compare against, and
	// "update from an installed copy that is not answering" is not a sentence
	// anybody should be shown. What it needs is a repair either way.
	case state.InstalledVersion == "":
		first = choice(actionRepair, "Repair this installation (Bokkie is installed here but is not answering)")
	case state.InstalledVersion != state.ThisVersion:
		first = choice(actionUpdate, "Update Bokkie from "+state.InstalledVersion+" to "+state.ThisVersion)
	default:
		first = choice(actionRepair, "Repair this installation (reinstall "+state.InstalledVersion+")")
	}

	return []setupChoice{
		first,
		shortcut,
		choice(actionUninstall, "Remove Bokkie from this computer (the data folder is kept)"),
		closeIt,
	}
}

// describeInstallation works out what this machine already has.
//
// The serving version is preferred over the file on disk because it is the one
// answering on the port — a binary sitting at the install path that is not
// running is a failed update, not an installation. Falling back to "installed
// but not answering" keeps the repair offer available in exactly that case.
func describeInstallation(exePath, installedPath, serving string, servingErr error) setupState {
	state := setupState{
		ThisVersion:            buildinfo.Version,
		Elevated:               winsetup.IsElevated(),
		RunningFromInstallPath: sameFile(exePath, installedPath),
	}

	switch {
	case servingErr == nil && serving != "":
		state.Installed, state.InstalledVersion = true, serving
	case fileExists(installedPath):
		// Installed, but with no version to report: the binary is there and
		// nothing is answering on the port.
		state.Installed = true
	}
	return state
}

// sameFile compares paths case-insensitively after cleaning, which is what
// Windows means by the same file for this purpose.
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// The names the elevated window is given on its command line. They are spelled
// once, here, because the unelevated window writes them and the elevated one
// reads them, and a disagreement between the two would be a window that
// silently does the wrong thing.
var actionNames = map[setupAction]string{
	actionInstall:   "install",
	actionUpdate:    "update",
	actionRepair:    "repair",
	actionShortcut:  "shortcut",
	actionUninstall: "uninstall",
}

func nameOf(a setupAction) string { return actionNames[a] }

// actionFor is nameOf backwards. Anything unrecognised is actionQuit rather
// than the zero value, which is actionInstall: a misspelling must never be
// read as the one action that turns a counter PC into a second host.
func actionFor(name string) setupAction {
	for action, known := range actionNames {
		if known == name {
			return action
		}
	}
	return actionQuit
}

// ask prints the menu and reads one number.
func ask(w io.Writer, r io.Reader, state setupState) (setupAction, error) {
	choices := menuFor(state)

	fmt.Fprintf(w, "\nBokkie %s\n\n", state.ThisVersion)
	if state.Installed {
		fmt.Fprintf(w, "This computer has Bokkie %s on it.\n\n", state.InstalledVersion)
	} else {
		fmt.Fprint(w, "This computer does not have Bokkie on it.\n\n")
	}
	for i, choice := range choices {
		fmt.Fprintf(w, "  %d. %s\n", i+1, choice.Label)
	}
	fmt.Fprint(w, "\nType a number and press Enter: ")

	var pick int
	if _, err := fmt.Fscanln(r, &pick); err != nil || pick < 1 || pick > len(choices) {
		// Not a failure worth a stack of words: they can run the file again.
		return actionQuit, nil
	}
	return choices[pick-1].Action, nil
}

// askAddress offers the host address the shortcut should point at.
func askAddress(w io.Writer, r io.Reader) (string, error) {
	fmt.Fprintf(w, "\nThe shortcut will open %s\n", winsetup.DefaultAddress)
	fmt.Fprint(w, "Press Enter to use that, or type the host computer's address\n")
	fmt.Fprint(w, "(for example http://192.168.1.50:8080): ")

	line, err := io.ReadAll(io.LimitReader(r, 512))
	if err != nil {
		return winsetup.DefaultAddress, nil
	}
	typed := strings.TrimSpace(string(line))
	if typed == "" {
		return winsetup.DefaultAddress, nil
	}
	return typed, nil
}

// programEntry is how Bokkie describes itself to Add/Remove Programs.
func programEntry(installedPath string) winsetup.Program {
	return winsetup.Program{
		Name:            service.DisplayName,
		Version:         buildinfo.Version,
		Publisher:       "Bokkie",
		InstallLocation: filepath.Dir(installedPath),
		Icon:            installedPath,
		UninstallExe:    installedPath,
		// --from-control-panel, because Windows runs this with no console to
		// print into and no one watching a terminal.
		UninstallArgs: []string{"uninstall", "--from-control-panel"},
	}
}

// setupOptions reuses the front end install and uninstall share, so the guided
// window resolves the same data directory and port they do.
func setupOptions() (service.Options, *slog.Logger, io.Closer, error) {
	return serviceCommand("setup", nil)
}
