//go:build windows

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Dannjann/bokkie-lms/internal/buildinfo"
	"github.com/Dannjann/bokkie-lms/internal/config"
	"github.com/Dannjann/bokkie-lms/internal/release"
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
	// The one line in this program that no test and no type-check can judge:
	// GetConsoleProcessList is declared by hand, and if it returns 0 on a real
	// console then a double-click shows usage in a window that vanishes —
	// which is the exact failure this whole step exists to close. This verb
	// lets the Windows runner call it and look at the answer.
	probeConsole := fs.Bool("probe-console", false, "print how many processes share this console, and do nothing else")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *probeConsole {
		fmt.Println(winsetup.ConsoleProcessCount())
		return nil
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
	ctx := context.Background()

	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	installed, err := service.InstalledPath()
	if err != nil {
		return "", err
	}

	// config.Load reads; it does not create. Nothing here may create anything
	// until somebody has chosen an action: most people running this file are at
	// a counter PC that wants a desktop icon, and both the runbook and the
	// download page promise them that path installs nothing.
	cfg, err := config.Load("")
	if err != nil {
		return "", err
	}
	serving, servingErr := service.ServingVersion(cfg.Port)
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

	// Review Focus 4, and it is checked here rather than in the switch below so
	// that nobody is made to approve an administrator prompt for an action that
	// was never going to run. placeExecutable guards it a second time.
	if state.RunningFromInstallPath && (action == actionInstall || action == actionRepair || action == actionUpdate) {
		return "", winsetup.Verdict("This is the copy of Bokkie that is already installed on this computer.\n\n" +
			"Run the file you downloaded instead. Nothing has been changed.")
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
		// weighSelf decides whether this file may touch the machine at all.
		// Its refusal is returned, not printed: printing it and carrying on is
		// how a screen comes to say "nothing has been changed" immediately
		// before the service is torn down and replaced.
		if err := permitted(ctx, state, exe); err != nil {
			return "", err
		}
		opts, log, closeLog, err := setupOptions()
		if err != nil {
			return "", err
		}
		defer closeLog.Close()

		// The same path the USB stick's Install Bokkie.cmd takes, so both ways
		// of installing leave the machine in the same state — Control Panel
		// entry included. service.Install deregisters the existing service
		// before it replaces the binary, so a failure inside here is a partial
		// change, which installHere marks.
		if err := installHere(log, opts); err != nil {
			return "", err
		}
		return winsetup.DoneInstalled, nil

	case actionUpdate:
		if err := permitted(ctx, state, exe); err != nil {
			return "", err
		}
		_, log, closeLog, err := setupOptions()
		if err != nil {
			return "", err
		}
		defer closeLog.Close()

		// update.Apply takes its own backup and rolls itself back, so a failure
		// inside it is not a partial change in the sense ErrPartial means.
		if err := applyDownloadedUpdate(ctx, log, exe); err != nil {
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
		if err := uninstall("", false); err != nil {
			return "", err
		}
		return winsetup.DoneRemoved, nil
	}
	return "", nil
}

// applyDownloadedUpdate replaces the installed binary with this file.
//
// It stages a release folder in a temporary directory — this executable under
// the name the manifest gives it, beside the manifest and signature fetched
// from the same release — and hands that folder to the update the USB path
// uses. Everything careful then happens in code that has not changed: the
// signature, the backup, the swap, the health check, and the rollback when the
// new binary does not come up.
func applyDownloadedUpdate(ctx context.Context, log *slog.Logger, exe string) error {
	dir, err := os.MkdirTemp("", "bokkie-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	if err := release.DownloadManifest(ctx, nil, release.LatestBase, dir); err != nil {
		return err
	}
	manifest, err := release.Load(dir)
	if err != nil {
		return err
	}
	artifact, err := manifest.ForHost()
	if err != nil {
		return err
	}

	if err := copyTo(filepath.Join(dir, artifact.File), exe); err != nil {
		return err
	}

	result, err := applyRelease(ctx, dir, "", false)
	if err != nil {
		return err
	}
	log.Info("updated", "from", result.From, "to", result.To, "backup", result.BackupPath)
	return nil
}

// permitted asks whether this file may touch the machine, and turns a refusal
// into the error the window prints.
//
// One function so that install, repair and update cannot drift apart on the
// question — the state they are in differs, the rule does not.
func permitted(ctx context.Context, state setupState, exe string) error {
	check, checkErr := verifySelf(ctx, nil, release.LatestBase, exe, buildinfo.Version)

	proceed, notice := weighSelf(state.Installed, check, checkErr)
	if !proceed {
		return notice
	}
	if notice != nil {
		// Allowed, with something worth saying first.
		fmt.Println()
		fmt.Println(notice.Error())
	}
	return nil
}

// copyTo writes src to dst. The staged copy is a new file rather than a link or
// a rename, so that whatever Windows has attached to the download — the
// mark-of-the-web an internet download carries — is left behind with it.
func copyTo(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
