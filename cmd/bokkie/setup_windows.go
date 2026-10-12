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
	ctx := context.Background()

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
		// A first install proceeds even unchecked — an offline station has
		// nothing on it to lose, and refusing here is what would make the USB
		// fallback useless. It says so rather than staying quiet.
		if check, checkErr := verifySelf(ctx, nil, release.LatestBase, exe, buildinfo.Version); !check.OK {
			if checkErr == nil {
				checkErr = errors.New(check.Reason)
			}
			if _, message := selfCheckOutcome(state.Installed, false, checkErr); message != "" {
				fmt.Println()
				fmt.Println(message)
			}
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
		// The asymmetry: an installed station is not replaced by a file that
		// could not be checked. selfCheckOutcome holds the reasoning.
		check, checkErr := verifySelf(ctx, nil, release.LatestBase, exe, buildinfo.Version)
		if !check.OK {
			if checkErr == nil {
				checkErr = errors.New(check.Reason)
			}
			if _, message := selfCheckOutcome(true, false, checkErr); message != "" {
				return "", errors.New(message)
			}
		}
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
		if err := service.Uninstall(log, opts); err != nil {
			return "", err
		}
		return winsetup.DoneRemoved, winsetup.Deregister()
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
