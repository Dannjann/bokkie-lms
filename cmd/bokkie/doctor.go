package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/Dannjann/bokkie-lms/internal/config"
	"github.com/Dannjann/bokkie-lms/internal/db"
	"github.com/Dannjann/bokkie-lms/internal/diagnose"
	"github.com/Dannjann/bokkie-lms/internal/service"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// cmdDoctor answers "why can nobody reach this?" without a support call.
//
// It is the command to give somebody over the phone. It changes nothing, reads
// no password, and prints the whole picture at once — which is the difference
// between one call and six.
func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "override the per-OS data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	report := diagnoseHost(*dataDir)
	fmt.Print(report)
	if !report.Healthy() {
		os.Exit(1)
	}
	return nil
}

// diagnoseHost gathers what the checks need and runs them.
//
// Everything it reads is optional. A station whose database cannot be opened
// still gets a report about whether it is serving and whether its name
// resolves — which is exactly the station most in need of one.
func diagnoseHost(dataDir string) diagnose.Report {
	opts := diagnose.Options{Hostname: "bokkie", Port: 8080}

	cfg, err := config.Load(dataDir)
	if err == nil {
		opts.Hostname = cfg.MDNS.Hostname
		opts.Port = cfg.Port
	}
	opts.LocalAddresses = diagnose.LocalIPv4()
	opts.StartHint = startHint()
	if err == nil {
		opts.LogDir = cfg.LogDir()
	}
	opts.Serving, opts.ServingErr = service.ServingVersion(opts.Port)
	opts.PortOpen = diagnose.PortOpen(opts.Port)
	if err == nil {
		opts.ConfigPath = cfg.ConfigPath()
	}

	// Only worth opening the database if something is answering: a station
	// that is down has a more pressing problem than which way a switch is set.
	if opts.ServingErr == nil && err == nil {
		if lan, ok := readLANAccess(cfg); ok {
			opts.LANAccess, opts.LANAccessKnown = lan, true
		}
	}
	return diagnose.Run(opts)
}

// readLANAccess reads the one setting that changes who is served. A failure is
// not reported as a fault — the report says the setting could not be read and
// moves on, because a locked database is a different story from a switch.
func readLANAccess(cfg config.Config) (bool, bool) {
	ctx := context.Background()

	database, err := db.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return false, false
	}
	defer database.Close()

	store, err := settings.Open(ctx, database)
	if err != nil {
		return false, false
	}
	return store.Bool(settings.LANAccess), true
}

// startHint is the command that starts the service on this host. The three are
// spelled out rather than derived because they are what somebody types, and a
// runbook that is right about two platforms is a runbook nobody trusts.
func startHint() string {
	switch runtime.GOOS {
	case "windows":
		return "sc start bokkie"
	case "darwin":
		return "sudo launchctl bootstrap system /Library/LaunchDaemons/bokkie.plist"
	default:
		return "sudo systemctl start bokkie"
	}
}
