package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/Dannjann/bokkie-lms/internal/diagnose"
	"github.com/Dannjann/bokkie-lms/internal/service"
	"github.com/Dannjann/bokkie-lms/internal/winsetup"
)

// cmdInstall registers bokkie with the host's service manager, opens the
// firewall and starts it — one command on all three platforms, because the
// person running it is usually standing at the station with the owner waiting.
func cmdInstall(args []string) error {
	opts, log, closeLog, err := serviceCommand("install", args)
	if err != nil {
		return err
	}
	defer closeLog.Close()

	if err := installHere(log, opts); err != nil {
		return err
	}

	log.Info("bokkie is installed and running",
		"url", "http://bokkie.local:"+itoa(opts.Port),
		"next", "reboot the host and confirm the app still answers with nobody logged in")
	return nil
}

// installHere registers the service and, on Windows, puts bokkie in Add/Remove
// Programs.
//
// Both ways of installing go through here. The registry write used to live in
// the setup window alone, which meant a station installed from the USB stick —
// Install Bokkie.cmd, which calls this verb — never appeared in Add/Remove
// Programs at all, while the runbook promised it did. The Windows runner found
// it by checking the promise rather than the path that makes it.
func installHere(log *slog.Logger, opts service.Options) error {
	if err := portIsOurs(opts.Port); err != nil {
		return err
	}
	if err := service.Install(log, opts); err != nil {
		return winsetup.Partial(err)
	}

	installed, err := service.InstalledPath()
	if err != nil {
		return err
	}
	if err := winsetup.Register(programEntry(installed)); err != nil {
		// A missing Control Panel entry is cosmetic beside a working station,
		// and failing an install over it would be the tail wagging the dog.
		log.Warn("could not add Bokkie to Add/Remove Programs", "error", err)
	}

	reportReachability(log, opts.DataDir)
	return nil
}

// portIsOurs refuses an install onto a port another program already owns.
//
// Refusing here follows the rule the rest of the installer already follows: a
// refusal happens before anything on the host changes. Without it the service
// registers, fails to bind, and leaves a machine with a service that cannot
// start and a person reading "the service is not answering" — which describes
// the symptom and names nothing about the cause.
//
// A port held by Bokkie itself is not a clash. That is a repair or an update,
// which is most of what this path is used for.
func portIsOurs(port int) error {
	if !diagnose.PortOpen(port) {
		return nil
	}
	if _, err := service.ServingVersion(port); err == nil {
		return nil
	}
	return fmt.Errorf("another program on this computer is already using port %d, so Bokkie cannot start there.\n"+
		"Nothing has been changed.\n\n"+
		"Either stop that program, or give Bokkie a different port by editing the\n"+
		"port line in the settings file in Bokkie's data folder, then install again.\n"+
		"Run \"bokkie doctor\" to see what is on the port", port)
}

// reportReachability checks, from the host and at the end of every install,
// that the name in the shortcut on every counter PC actually resolves to this
// machine and answers.
//
// It is printed, never fatal. The install genuinely succeeded: the service is
// registered, running and answering on its own address, and a station that
// serves fine but cannot advertise a name is a working station. What it must
// not do is say "Bokkie is installed, open http://bokkie.local:8080" while
// that address goes nowhere — which is how the first real install was reported
// as broken when it was not.
func reportReachability(log *slog.Logger, dataDir string) {
	report := diagnoseHost(dataDir)
	if report.Healthy() {
		log.Info("the station is reachable", "address", report.Reach)
		return
	}

	fmt.Println()
	fmt.Println("Bokkie is installed and running, but not everything about the")
	fmt.Println("network checked out:")
	fmt.Println()
	fmt.Print(report)
	fmt.Println("Run \"bokkie doctor\" at any time to check this again.")
}

// cmdUninstall stops and deregisters the service. The data directory and the
// service account stay, so an uninstall typed at the wrong moment is not a data
// loss event.
//
// --from-control-panel is set by the Add/Remove Programs entry. Windows runs
// that command with no console behind it, so anything printed goes nowhere and
// a silent exit looks to the person who pressed Uninstall like nothing happened
// at all.
func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "override the per-OS data directory")
	fromControlPanel := fs.Bool("from-control-panel", false, "report the outcome in a window rather than on a console")
	if err := fs.Parse(args); err != nil {
		return err
	}

	err := uninstall(*dataDir, *fromControlPanel)

	// Every exit from the Control Panel path goes through here, including the
	// ones that happen before there is a log to write to. A dialog that closes
	// and shows nothing at all is the failure D6 was written about, and the
	// earliest returns — a missing data folder, a log file that cannot be
	// opened — are exactly the ones that would otherwise take that route.
	if *fromControlPanel {
		if errors.Is(err, winsetup.ErrElevationCancelled) {
			return nil
		}
		winsetup.Report("Bokkie", winsetup.ExitMessage(err, winsetup.DoneRemoved))
	}
	return err
}

func uninstall(dataDir string, fromControlPanel bool) error {
	// Windows runs an UninstallString unelevated, and nothing about this
	// binary makes it ask: UAC's installer-detection heuristics only apply to
	// 32-bit executables without a manifest. Without this, every uninstall
	// from Add/Remove Programs fails on "access is denied" — which is what the
	// runbook currently promises works.
	if fromControlPanel && !winsetup.IsElevated() {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return winsetup.Elevate(exe, []string{"uninstall", "--from-control-panel"})
	}

	opts, log, closeLog, err := serviceOptions(dataDir)
	if err != nil {
		return err
	}
	defer closeLog.Close()

	if err := service.Uninstall(log, opts); err != nil {
		return err
	}
	if err := winsetup.Deregister(); err != nil {
		// The service is gone and Control Panel still lists it: a partial
		// state, and the message has to say so rather than claim nothing
		// happened.
		return winsetup.Partial(err)
	}
	return nil
}
