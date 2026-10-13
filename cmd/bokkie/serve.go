package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/app"
)

// shutdownWait bounds the drain. It is longer than any request this system
// serves and far shorter than a person's patience with a window that will not
// close.
const shutdownWait = 10 * time.Second

// cmdServe runs the station in this window. Closing the window stops it, and
// nothing else starts it — see docs/plans/02.7-window-is-the-server.md.
func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dataDir := fs.String("data-dir", "", "override the per-OS data directory")
	noWindow := fs.Bool("no-window", false, "do not open a window; this console is the switch")
	dev := fs.Bool("dev", false, "serve the frontend from the Vite dev server instead of the embedded build")
	viteURL := fs.String("vite-url", defaultViteURL, "address of the Vite dev server, with -dev")
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts, err := runOptions(*dev, *viteURL)
	if err != nil {
		return err
	}

	cfg, log, closeLog, err := bootstrap(*dataDir, true)
	if err != nil {
		return err
	}
	defer closeLog.Close()

	server, err := app.New(context.Background(), cfg, log, opts)
	if err != nil {
		return err
	}

	// Task 2 replaces this with a channel the window's own closing closes.
	stop := make(chan struct{})
	_ = noWindow

	return serveUntil(context.Background(), log, server, stop)
}

// serveUntil serves until something says to stop, then shuts down cleanly.
//
// The reason to stop arrives as a channel rather than being discovered here:
// Ctrl+C, a closed console, and the window being closed are three different
// events that must all end in the same drain, and a function that knew about
// any one of them could not be tested without producing it.
func serveUntil(ctx context.Context, log *slog.Logger, server *app.App, stop <-chan struct{}) error {
	if err := server.Start(); err != nil {
		return err
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case <-stop:
		log.Info("the window was closed; stopping")
	case sig := <-signals:
		log.Info("stopping", "signal", sig)
	case <-ctx.Done():
	}

	shutdown, cancel := context.WithTimeout(context.Background(), shutdownWait)
	defer cancel()
	return server.Stop(shutdown)
}
