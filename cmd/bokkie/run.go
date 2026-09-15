package main

import (
	"flag"
	"fmt"

	"github.com/Dannjann/bokkie-lms/internal/buildinfo"
	"github.com/Dannjann/bokkie-lms/internal/config"
)

// cmdRun boots the server in the foreground. It is built up across step 1:
// configuration here (1.2), then logging (1.3), database (1.4) and HTTP (1.5).
func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "override the per-OS data directory")
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

	// Half of remote debugging is discovering the configuration is not what
	// someone assumed, so the resolved values are reported before anything
	// uses them. This becomes a structured boot log line in task 1.3.
	fmt.Println(buildinfo.String())
	fmt.Println("config file:", cfg.ConfigPath())
	fmt.Println("data dir:   ", cfg.DataDir)
	fmt.Println("database:   ", cfg.DatabasePath())
	fmt.Println("backups:    ", cfg.Backup.Folder, fmt.Sprintf("(keep %d)", cfg.Backup.Retention))
	fmt.Println("port:       ", cfg.Port)
	fmt.Println("timezone:   ", cfg.Timezone)
	fmt.Println("slip queue: ", printerQueue(cfg))

	return notImplemented("run", "1.5 — HTTP server")
}

func printerQueue(cfg config.Config) string {
	if cfg.Printer.Queue == "" {
		return "(none configured — slip printing disabled)"
	}
	return cfg.Printer.Queue
}
