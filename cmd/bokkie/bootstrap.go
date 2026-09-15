package main

import (
	"io"
	"log/slog"

	"github.com/Dannjann/bokkie-lms/internal/buildinfo"
	"github.com/Dannjann/bokkie-lms/internal/config"
	"github.com/Dannjann/bokkie-lms/internal/logging"
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
