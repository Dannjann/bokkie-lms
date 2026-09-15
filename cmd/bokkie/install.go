package main

import "github.com/Dannjann/bokkie-lms/internal/service"

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
func cmdUninstall(args []string) error {
	opts, log, closeLog, err := serviceCommand("uninstall", args)
	if err != nil {
		return err
	}
	defer closeLog.Close()

	return service.Uninstall(log, opts)
}
