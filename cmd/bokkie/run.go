package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/Dannjann/bokkie-lms/internal/app"
)

// defaultViteURL is where `make web-dev` puts the Vite dev server.
const defaultViteURL = "http://localhost:5173"

// cmdRun runs the server in the foreground until interrupted. The service
// manager path in task 1.7 does not come through here — it drives the same
// app.Start and app.Stop directly, because a Windows service stop is not a
// signal.
func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "override the per-OS data directory")
	console := fs.Bool("console", true, "also write logs to stderr")
	dev := fs.Bool("dev", false, "serve the frontend from the Vite dev server instead of the embedded build")
	viteURL := fs.String("vite-url", defaultViteURL, "address of the Vite dev server, with -dev")
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts, err := runOptions(*dev, *viteURL)
	if err != nil {
		return err
	}

	cfg, log, closeLog, err := bootstrap(*dataDir, *console)
	if err != nil {
		return err
	}
	defer closeLog.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server, err := app.New(ctx, cfg, log, opts)
	if err != nil {
		return err
	}
	return server.Run(ctx)
}

// runOptions turns the -dev flag into the one choice it controls: whether the
// frontend comes from the Vite dev server or from the copy embedded in this
// binary.
func runOptions(dev bool, viteURL string) (app.Options, error) {
	if !dev {
		return app.Options{}, nil
	}
	target, err := url.Parse(viteURL)
	if err != nil || target.Host == "" {
		return app.Options{}, fmt.Errorf("-vite-url %q is not a valid address", viteURL)
	}
	return app.Options{DevProxy: target}, nil
}
