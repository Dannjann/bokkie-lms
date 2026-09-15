package main

import (
	"flag"
	"log/slog"

	"github.com/Dannjann/bokkie-lms/internal/buildinfo"
	"github.com/Dannjann/bokkie-lms/internal/config"
	"github.com/Dannjann/bokkie-lms/internal/logging"
)

// cmdRun boots the server in the foreground. It is built up across step 1:
// configuration (1.2), logging here (1.3), then database (1.4) and HTTP (1.5).
func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "override the per-OS data directory")
	console := fs.Bool("console", true, "also write logs to stderr (off under a service manager)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*dataDir)
	if err != nil {
		return err
	}
	if err := config.EnsureDirs(cfg); err != nil {
		return err
	}

	log, closeLog, err := logging.New(logging.Options{
		Dir:       cfg.LogDir(),
		Retention: cfg.Log.Retention,
		Level:     cfg.LogLevel(),
		Console:   *console,
	})
	if err != nil {
		return err
	}
	defer closeLog.Close()
	slog.SetDefault(log)

	// Half of remote debugging is discovering the configuration is not what
	// someone assumed, so the resolved values are on the record before
	// anything uses them.
	log.Info("bokkie starting",
		"version", buildinfo.Version,
		"commit", buildinfo.Commit,
		"build", buildinfo.String(),
		"config_file", cfg.ConfigPath(),
		"config", cfg,
	)

	return notImplemented("run", "1.5 — HTTP server")
}
