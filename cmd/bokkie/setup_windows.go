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
	// A folder this computer cannot write is not a reason to refuse to show a
	// window: it is the one thing the window exists to offer to fix. The port
	// falls back to the default so the rest of the state can still be read.
	cfg, cfgErr := config.Load("")
	port := defaultPort
	if cfgErr == nil {
		port = cfg.Port
	}

	serving, servingErr := service.ServingVersion(port)
	state := describeInstallation(exe, installed, serving, servingErr)
	if cfgErr == nil {
		state.OldServiceInstalled = oldServiceInstalled(cfg)
		state.DataFolder = cfg.DataDir
	} else if folder, ok := config.UnwritableFolder(cfgErr); ok {
		state.DataFolder, state.DataFolderUnwritable = folder, true
		fmt.Println()
		fmt.Println(cfgErr.Error())
	} else {
		return "", cfgErr
	}

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
	if state.RunningFromInstallPath && action == actionUpdate {
		return "", winsetup.Verdict("This is the copy of Bokkie that is already installed on this computer.\n\n" +
			"Run the file you downloaded instead. Nothing has been changed.")
	}

	// Anything needing rights we do not have goes to a second window. The
	// shortcut deliberately does not, which is why this is checked per choice
	// rather than once at the top.
	if needsAdmin(action) && !elevated && !winsetup.IsElevated() {
		if err := winsetup.Elevate(exe, winsetup.ElevatedArgs(nameOf(action))); err != nil {
			// Declining the prompt for the service removal is the one refusal
			// that must not be generic. Somebody who is told only "that did not
			// work" goes on to start the station in its window, reads "Bokkie
			// runs only while this window is open", and believes it — while the
			// machine still starts the old service on every reboot.
			if action == actionRemoveService {
				return "", winsetup.Verdict(
					"The old Bokkie service was NOT removed, so this computer will still\n" +
						"start Bokkie by itself after a restart, whether or not anybody\n" +
						"opened it.\n\n" +
						"Run this file again and allow the prompt. Nothing else has been\n" +
						"changed.")
			}
			return "", err
		}
		return "", errHandedOff
	}

	switch action {
	case actionFixDataFolder:
		// The folder is read from the configuration again rather than carried
		// here on a command line: the elevated child re-runs the same lookup
		// and gets the same answer, which is one fewer thing to quote and one
		// fewer thing to get wrong.
		folder, ok := config.UnwritableFolder(cfgErr)
		if !ok {
			return "There is nothing to fix: Bokkie can already write its records.", nil
		}
		if err := winsetup.Grant(folder); err != nil {
			return "", err
		}
		return "Bokkie can now write its records in\n    " + folder + "\n\n" +
			"Nothing was changed inside that folder. Open Bokkie again the\n" +
			"normal way — it will not need permission after this.", nil

	case actionAlreadyRunning:
		return "Bokkie is already running on this computer.\n\n" +
			"Look for its window — it is the one you close to stop the station.\n" +
			"If you cannot find it, restart this computer and open Bokkie again.", nil

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

	case actionDoctor:
		// Deliberately needs no administrator rights and changes nothing: the
		// moment to run this is when something is wrong, and a prompt in the
		// way is one more thing between the person and the answer.
		fmt.Println()
		fmt.Print(diagnoseHost(""))
		return "", nil

	case actionServe:
		// The station runs here, in this window, until it is closed. Nothing
		// is installed and nothing is registered — a person opening this is
		// the only way the station is ever served.
		return "", cmdServe(nil)

	case actionRemoveService:
		return "", removeOldService()

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
// A station that is running is one with something to lose, which is the
// distinction weighSelf's asymmetry turns on.
func permitted(ctx context.Context, state setupState, exe string) error {
	check, checkErr := verifySelf(ctx, nil, release.LatestBase, exe, buildinfo.Version)

	proceed, notice := weighSelf(state.Running, check, checkErr)
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

// oldServiceInstalled reports whether a 2.6 install left a service behind.
//
// Such a service starts the station on its own when the computer restarts,
// which is the one thing this step exists to stop, so a machine still carrying
// one is offered its removal wherever it appears in the menu.
func oldServiceInstalled(cfg config.Config) bool {
	installed, err := service.IsInstalled(service.Options{
		DataDir: cfg.DataDir,
		Port:    cfg.Port,
		LogDir:  cfg.LogDir(),
	})
	return err == nil && installed
}

// removeOldService takes away the service a 2.6 install left behind.
//
// Without this the one machine being upgraded is the one machine that still
// starts on its own — the hard requirement violated on the only host there is.
func removeOldService() error {
	opts, log, closeLog, err := setupOptions()
	if err != nil {
		return err
	}
	defer closeLog.Close()

	installed, err := service.IsInstalled(opts)
	if err != nil {
		return err
	}
	if !installed {
		fmt.Println()
		fmt.Println("There is no Bokkie service on this computer. Nothing to remove.")
		return nil
	}

	fmt.Println()
	fmt.Println("This computer still has the older Bokkie installed as a Windows service,")
	fmt.Println("which starts on its own when the computer restarts. This version runs")
	fmt.Println("only while its window is open, so that service is in the way.")
	fmt.Println()

	if err := service.Uninstall(log, opts); err != nil {
		// The person has to know the old one is still there, or they will
		// believe the station only runs when they open it while it does not.
		fmt.Println("The old service was NOT removed, so this computer will still start")
		fmt.Println("Bokkie by itself after a restart. Run this file again as an")
		fmt.Println("administrator and choose the same option.")
		return err
	}
	if err := winsetup.Deregister(); err != nil {
		log.Warn("the service is gone but the Add/Remove Programs entry is not", "error", err)
	}

	fmt.Println("The old service has been removed. Bokkie now runs only while its window")
	fmt.Println("is open, and nothing starts it on its own.")
	return nil
}
