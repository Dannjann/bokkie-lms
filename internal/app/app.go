// Package app wires the running system together and owns its lifecycle.
//
// Start and Stop are deliberately separate from any command line: task 1.7 has
// the service manager call them directly, and a Windows service stop is not a
// SIGTERM. Keeping the lifecycle here means there is one shutdown path, whether
// it was triggered by Ctrl-C, systemd, launchd, or the Windows SCM.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/api"
	"github.com/Dannjann/bokkie-lms/internal/audit"
	"github.com/Dannjann/bokkie-lms/internal/auth"
	"github.com/Dannjann/bokkie-lms/internal/backup"
	"github.com/Dannjann/bokkie-lms/internal/config"
	"github.com/Dannjann/bokkie-lms/internal/db"
	"github.com/Dannjann/bokkie-lms/internal/discovery"
	"github.com/Dannjann/bokkie-lms/internal/settings"
	"github.com/Dannjann/bokkie-lms/web"
)

// shutdownGrace is how long in-flight requests get to finish before the server
// stops waiting. Long enough for a report to finish rendering, short enough
// that a reboot is not held up by a stuck client.
const shutdownGrace = 15 * time.Second

// readHeaderTimeout defends against a client that opens a connection and never
// finishes its request. There is deliberately no write timeout: a long report
// download is a legitimate slow response, and cutting it off mid-stream would
// look like a corrupt file.
const readHeaderTimeout = 10 * time.Second

// App is a configured, not-yet-listening system.
type App struct {
	cfg config.Config
	log *slog.Logger
	db  *sql.DB
	srv *http.Server

	backups *backup.Scheduler

	listener net.Listener
	serveErr chan error
	mdns     io.Closer
}

// Options are the run-time choices that are not configuration: things decided
// by how the process was started rather than by config.yaml.
type Options struct {
	// DevProxy, when set, serves the frontend from the Vite dev server instead
	// of the copy embedded in this binary.
	DevProxy *url.URL
}

// New opens the database, brings the schema up to date and builds the HTTP
// handler. It does not listen — that is Start, so a port already in use is
// reported by whoever asked for the server to start.
func New(ctx context.Context, cfg config.Config, log *slog.Logger, opts Options) (*App, error) {
	database, err := db.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, database, log); err != nil {
		database.Close()
		return nil, err
	}

	store, err := settings.Open(ctx, database)
	if err != nil {
		database.Close()
		return nil, err
	}

	spa, err := web.FS()
	if err != nil {
		database.Close()
		return nil, fmt.Errorf("open embedded frontend: %w", err)
	}

	sessions := auth.NewSessions(database)
	// Dead session rows accumulate over the years this machine runs. Startup is
	// the natural moment to sweep them: nothing is serving yet, so a long
	// delete cannot block a request.
	if n, err := sessions.DeleteExpired(ctx); err != nil {
		log.Warn("could not clear expired sessions", "error", err)
	} else if n > 0 {
		log.Info("cleared expired sessions", "count", n)
	}

	auditLog := audit.New(database, log)
	backups := backup.NewScheduler(database, store, cfg.Backup.Folder, log)

	handler := api.NewHandler(api.Deps{
		Logger:       log,
		DB:           database,
		Started:      time.Now(),
		Users:        auth.NewUsers(database),
		Sessions:     sessions,
		Limiter:      auth.NewLimiter(),
		Audit:        auditLog,
		Settings:     store,
		Backup:       backups,
		BackupFolder: cfg.Backup.Folder,
		SPA:          spa,
		DevProxy:     opts.DevProxy,
	})

	return &App{
		cfg:     cfg,
		log:     log,
		db:      database,
		backups: backups,
		srv: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       60 * time.Second,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		},
		serveErr: make(chan error, 1),
	}, nil
}

// Start binds the port and begins serving in the background. It returns once
// the listener is open, so a caller that gets a nil error knows the station can
// reach the app — a service manager that reports "started" while the port is
// still in use would be lying.
func (a *App) Start() error {
	// 0.0.0.0, not localhost: the whole point is the other counter PCs and the
	// phones on the station WiFi.
	addr := fmt.Sprintf("0.0.0.0:%d", a.cfg.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	a.listener = listener

	a.log.Info("http server listening", "addr", addr, "port", a.cfg.Port)
	a.startDiscovery()
	a.backups.Start()

	go func() {
		err := a.srv.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		a.serveErr <- err
	}()
	return nil
}

// startDiscovery publishes the LAN name. A failure here is logged and
// tolerated, never fatal: the app is still perfectly reachable by IP address,
// and refusing to serve the station because a name could not be advertised
// would be a worse outcome than the inconvenience it prevents.
func (a *App) startDiscovery() {
	if !a.cfg.MDNS.Enabled {
		a.log.Info("mDNS advertising is disabled; reach the server by IP address")
		return
	}

	closer, err := discovery.Advertise(a.log, a.cfg.MDNS.Hostname, a.cfg.Port)
	if err != nil {
		a.log.Warn("could not advertise over mDNS; reach the server by IP address instead",
			"hostname", a.cfg.MDNS.Hostname, "error", err)
		return
	}
	a.mdns = closer
}

// Stop drains in-flight requests, then closes the database. Requests are
// allowed to finish rather than being cut off: a half-written response to a
// payment entry is exactly the ambiguity this system exists to remove.
func (a *App) Stop(ctx context.Context) error {
	a.log.Info("shutting down")

	// Withdraw the LAN name first: for the rest of the shutdown the station
	// should be sent to a server that is still answering, not to this one.
	if a.mdns != nil {
		if err := a.mdns.Close(); err != nil {
			a.log.Warn("could not withdraw the mDNS records", "error", err)
		}
		a.mdns = nil
	}

	// Stop the nightly job before draining requests. A backup interrupted
	// part-way leaves a file that would fail its own verification, and waiting
	// out an in-flight VACUUM INTO is far cheaper than explaining a folder with
	// a corrupt backup in it.
	a.backups.Stop()

	ctx, cancel := context.WithTimeout(ctx, shutdownGrace)
	defer cancel()

	shutdownErr := a.srv.Shutdown(ctx)
	if errors.Is(shutdownErr, context.DeadlineExceeded) {
		a.log.Warn("in-flight requests did not finish within the grace period; closing anyway",
			"grace", shutdownGrace)
		shutdownErr = a.srv.Close()
	}

	// The serve goroutine only returns once Shutdown or Close has run.
	if err := <-a.serveErr; err != nil && shutdownErr == nil {
		shutdownErr = err
	}
	if err := a.db.Close(); err != nil && shutdownErr == nil {
		shutdownErr = err
	}

	a.log.Info("stopped")
	return shutdownErr
}

// Addr is the address the server actually bound, which is what the caller needs
// when the configured port was 0 in a test.
func (a *App) Addr() net.Addr {
	if a.listener == nil {
		return nil
	}
	return a.listener.Addr()
}
