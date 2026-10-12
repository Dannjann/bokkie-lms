package main

import (
	"flag"
	"io"
	"log/slog"
	"strconv"

	"github.com/Dannjann/bokkie-lms/internal/buildinfo"
	"github.com/Dannjann/bokkie-lms/internal/config"
	"github.com/Dannjann/bokkie-lms/internal/logging"
	"github.com/Dannjann/bokkie-lms/internal/service"
)

// bootstrap resolves configuration and opens the log file — everything that has
// to happen before there is anywhere to report a failure to. It is shared by
// `run` and, from task 1.7, by the service entry point, so both paths read the
// same configuration and write to the same log.
//
// The returned io.Closer must be closed by the caller on shutdown.
func bootstrap(dataDir string, console bool) (config.Config, *slog.Logger, io.Closer, error) {
	cfg, err := config.Load(dataDir)
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	if err := config.EnsureDirs(cfg); err != nil {
		return config.Config{}, nil, nil, err
	}

	log, closeLog, err := logging.New(logging.Options{
		Dir:       cfg.LogDir(),
		Retention: cfg.Log.Retention,
		Level:     cfg.LogLevel(),
		Console:   console,
	})
	if err != nil {
		return config.Config{}, nil, nil, err
	}
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

	return cfg, log, closeLog, nil
}

// serviceCommand is the shared front end of `install` and `uninstall`: both
// take only --data-dir, and both need configuration and a log before they can
// report anything. The installed service is told the *resolved* data directory
// rather than the flag, so it reads the same one the installer did even though
// it runs as a different account with a different environment.
func serviceCommand(name string, args []string) (service.Options, *slog.Logger, io.Closer, error) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "override the per-OS data directory")
	if err := fs.Parse(args); err != nil {
		return service.Options{}, nil, nil, err
	}
	return serviceOptions(*dataDir)
}

// serviceOptions is serviceCommand without the flag parsing, for the callers
// that have their own flags — uninstall, which also takes --from-control-panel,
// and the setup window, which takes none.
func serviceOptions(dataDir string) (service.Options, *slog.Logger, io.Closer, error) {
	cfg, log, closeLog, err := bootstrap(dataDir, true)
	if err != nil {
		return service.Options{}, nil, nil, err
	}
	return service.Options{
		DataDir: cfg.DataDir,
		Port:    cfg.Port,
		LogDir:  cfg.LogDir(),
	}, log, closeLog, nil
}

// itoa keeps the call sites reading as prose.
func itoa(n int) string { return strconv.Itoa(n) }
