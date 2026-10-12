package main

import (
	"flag"

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

	opts, log, closeLog, err := serviceOptions(*dataDir)
	if err != nil {
		return err
	}
	defer closeLog.Close()

	err = service.Uninstall(log, opts)
	if err == nil {
		err = winsetup.Deregister()
	}
	if *fromControlPanel {
		winsetup.Report("Bokkie", winsetup.ExitMessage(err, winsetup.DoneRemoved))
	}
	return err
}
