package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/Dannjann/bokkie-lms/internal/app"
)

// cmdRun runs the server in the foreground until interrupted. The service
// manager path in task 1.7 does not come through here — it drives the same
// app.Start and app.Stop directly, because a Windows service stop is not a
// signal.
func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "override the per-OS data directory")
	console := fs.Bool("console", true, "also write logs to stderr")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, closeLog, err := bootstrap(*dataDir, *console)
	if err != nil {
		return err
	}
	defer closeLog.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server, err := app.New(ctx, cfg, log)
	if err != nil {
		return err
	}
	return server.Run(ctx)
}
