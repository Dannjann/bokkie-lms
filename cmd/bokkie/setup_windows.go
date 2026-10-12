//go:build windows

package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/Dannjann/bokkie-lms/internal/service"
	"github.com/Dannjann/bokkie-lms/internal/winsetup"
)

// errHandedOff means the work is now the elevated window's, and this one should
// close without claiming anything happened.
var errHandedOff = errors.New("handed off to the administrator window")

// runGuidedSetup is the window a double-click opens.
//
// It runs twice per install: once unelevated, to show the menu and find out
// what is wanted, and once as administrator with --do naming the answer. The
// parts that need no administrator — the desktop shortcut — are done in the
// first pass, before any prompt is raised.
func runGuidedSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	elevated := fs.Bool("elevated", false, "this process was started by the unelevated window")
	do := fs.String("do", "", "the action chosen in the unelevated window")
	if err := fs.Parse(args); err != nil {
		return err
	}

	done, err := guided(*elevated, *do)

	// errHandedOff is the one case that must stay silent: this window raised a
	// UAC prompt and the elevated copy now owns the job. Two windows holding
	// for Enter, one behind the other, is how an install looks hung.
	if errors.Is(err, errHandedOff) {
		return nil
	}

	fmt.Println()
	fmt.Println(winsetup.ExitMessage(err, done))
	winsetup.PressEnterToClose(os.Stdout, os.Stdin)
	return err
}

// guided is the whole window: find out what this machine has, offer what makes
// sense for it, and either do it or hand it to an elevated copy of ourselves.
func guided(elevated bool, do string) (string, error) {
	opts, log, closeLog, err := setupOptions()
	if err != nil {
		return "", err
	}
	defer closeLog.Close()

	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	installed, err := service.InstalledPath()
	if err != nil {
		return "", err
	}
	serving, servingErr := service.ServingVersion(opts.Port)
	state := describeInstallation(exe, installed, serving, servingErr)

	// The elevated child was told what to do and must not ask again: the
	// person answered that question in the window that raised the prompt.
	action := actionFor(do)
	if do == "" {
		if action, err = ask(os.Stdout, os.Stdin, state); err != nil {
			return "", err
		}
	}
	if action == actionQuit {
		return "Nothing was changed on this computer.", nil
	}

	// Anything needing rights we do not have goes to a second window. The
	// shortcut deliberately does not, which is why this is checked per choice
	// rather than once at the top.
	if needsAdmin(action) && !elevated && !winsetup.IsElevated() {
		if err := winsetup.Elevate(exe, winsetup.ElevatedArgs(nameOf(action))); err != nil {
			return "", err
		}
		return "", errHandedOff
	}

	switch action {
	case actionInstall, actionRepair:
		// Review Focus 4. placeExecutable would be copying a file over
		// itself, and the service manager is already pointed here.
		if state.RunningFromInstallPath {
			return "", fmt.Errorf("this is the copy of Bokkie that is already installed on this computer; " +
				"run the file you downloaded instead")
		}
		if err := service.Install(log, opts); err != nil {
			return "", err
		}
		if err := winsetup.Register(programEntry(installed)); err != nil {
			// A missing Control Panel entry is cosmetic beside a working
			// station, and refusing an install over it would be the tail
			// wagging the dog.
			log.Warn("could not add Bokkie to Add/Remove Programs", "error", err)
		}
		return winsetup.DoneInstalled, nil

	case actionUpdate:
		if err := applyDownloadedUpdate(log, opts, exe); err != nil {
			return "", err
		}
		return winsetup.DoneUpdated, nil

	case actionShortcut:
		address, err := askAddress(os.Stdout, os.Stdin)
		if err != nil {
			return "", err
		}
		if _, err := winsetup.CreateDesktopShortcut(address); err != nil {
			return "", err
		}
		return winsetup.DoneShortcut, nil

	case actionUninstall:
		if err := service.Uninstall(log, opts); err != nil {
			return "", err
		}
		return winsetup.DoneRemoved, winsetup.Deregister()
	}
	return "", nil
}

// applyDownloadedUpdate is filled in by task 6.
func applyDownloadedUpdate(log *slog.Logger, opts service.Options, exe string) error {
	return fmt.Errorf("task 6")
}
