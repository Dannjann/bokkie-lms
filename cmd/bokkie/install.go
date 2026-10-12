package main

import (
	"errors"
	"flag"
	"os"

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

	if err := service.Install(log, opts); err != nil {
		return err
	}

	log.Info("bokkie is installed and running",
		"url", "http://bokkie.local:"+itoa(opts.Port),
		"next", "reboot the host and confirm the app still answers with nobody logged in")
	return nil
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
