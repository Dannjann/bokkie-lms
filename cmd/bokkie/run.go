package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"

	"github.com/Dannjann/bokkie-lms/internal/app"
	"github.com/Dannjann/bokkie-lms/internal/service"
)

// defaultViteURL is where `make web-dev` puts the Vite dev server.
const defaultViteURL = "http://localhost:5173"

// cmdRun runs the server: in the foreground when it was typed at a terminal,
// and under the host's service manager when that started it. Both go through
// internal/service, so the lifecycle that gets exercised daily in development
// is the same one the station depends on — and a Windows service stop, which
// is not a signal, is handled by the same code path as Ctrl-C.
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

	server, err := app.New(context.Background(), cfg, log, opts)
	if err != nil {
		return err
	}
	return service.Run(log, server, service.Options{DataDir: cfg.DataDir, Port: cfg.Port})
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
