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
	"github.com/Dannjann/bokkie-lms/internal/domain/advance"
	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
	"github.com/Dannjann/bokkie-lms/internal/domain/interest"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/payment"
	"github.com/Dannjann/bokkie-lms/internal/domain/report"
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

	backups      *backup.Scheduler
	ledgerChecks *ledger.Checker

	listener net.Listener

	// The port 80 redirect, when port 80 was free. Both are nil when it was
	// not, which is a normal and uninteresting state.
	shortSrv      *http.Server
	shortListener net.Listener
	serveErr      chan error

	// store and discovery are held because LAN access is a setting now, not a
	// start-up decision: Start has to ask what it currently is, and the API has
	// to be able to move the LAN name when it changes.
	store     *settings.Store
	discovery *discovery.Publisher
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

	// The ledger checks itself against its own cache before the port is open,
	// so a balance that has drifted is corrected before anybody can be shown
	// it. A failure here is logged rather than fatal: a cache that could not be
	// verified is a reason to look at the logs, not a reason to leave the
	// station with no system at all — the entries themselves are untouched and
	// every balance can be rebuilt from them.
	ledgerSvc := ledger.New(database, store, auditLog, log)
	if err := ledgerSvc.CheckOnStart(ctx); err != nil {
		log.Error("could not verify the ledger cache at startup", "error", err)
	}

	publisher := discovery.NewPublisher(log, cfg.MDNS.Hostname, cfg.Port, cfg.MDNS.Enabled)

	handler := api.NewHandler(api.Deps{
		Logger:       log,
		DB:           database,
		Started:      time.Now(),
		Customers:    customer.New(database, auditLog, log),
		Advances:     advance.New(database, ledgerSvc, store, log),
		Payments:     payment.New(database, ledgerSvc, store, log),
		Interest:     interest.New(database, ledgerSvc, store, log),
		Reports:      report.New(database, ledgerSvc, log),
		Ledger:       ledgerSvc,
		Users:        auth.NewUsers(database),
		Sessions:     sessions,
		Limiter:      auth.NewLimiter(),
		Audit:        auditLog,
		Settings:     store,
		Discovery:    publisher,
		Backup:       backups,
		BackupFolder: cfg.Backup.Folder,
		SPA:          spa,
		DevProxy:     opts.DevProxy,
	})

	return &App{
		cfg:          cfg,
		log:          log,
		db:           database,
		store:        store,
		discovery:    publisher,
		backups:      backups,
		ledgerChecks: ledger.NewChecker(ledgerSvc, log),
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
	a.startShortAddress()
	a.startDiscovery()
	a.backups.Start()
	a.ledgerChecks.Start()

	go func() {
		err := a.srv.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		a.serveErr <- err
	}()
	return nil
}

// shortAddressPort is port 80, where a browser goes when it is given a bare
// name. It is not configurable: the whole value of it is that it is the port
// nobody has to be told about.
const shortAddressPort = 80

// startShortAddress also answers on port 80, so that typing bokkie.local does
// what the person expects.
//
// This exists because of a real evening: the first Windows install was reported
// as broken when somebody typed bokkie.local and the browser, having been given
// no port, went to 80, found nothing, and said "refused to connect" — which
// reads as a broken server rather than a missing port number. The runbook can
// say ":8080" as often as it likes; people type the name.
//
// Failing to bind is a log line and nothing more. Port 80 is commonly already
// taken — IIS, Skype of old, anything with a web console — and refusing to
// serve the station over a convenience would be the tail wagging the dog. The
// real port goes on working either way, which is what every shortcut uses.
func (a *App) startShortAddress() {
	if a.cfg.Port == shortAddressPort {
		return
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", shortAddressPort))
	if err != nil {
		a.log.Info("port 80 is not available, so the address needs its port number",
			"address", fmt.Sprintf("http://%s.local:%d", a.cfg.MDNS.Hostname, a.cfg.Port),
			"error", err)
		return
	}
	a.shortListener = listener

	// A redirect rather than a second copy of the app: one origin keeps the
	// session cookie, the audit log and anything else that reasons about where
	// a request came from working the way they already do.
	target := fmt.Sprintf(":%d", a.cfg.Port)
	a.shortSrv = &http.Server{
		ReadHeaderTimeout: readHeaderTimeout,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			http.Redirect(w, r, "http://"+host+target+r.URL.RequestURI(), http.StatusTemporaryRedirect)
		}),
		ErrorLog: slog.NewLogLogger(a.log.Handler(), slog.LevelWarn),
	}

	a.log.Info("also answering on the short address",
		"short", fmt.Sprintf("http://%s.local", a.cfg.MDNS.Hostname),
		"app", fmt.Sprintf("http://%s.local:%d", a.cfg.MDNS.Hostname, a.cfg.Port))
	go a.shortSrv.Serve(listener)
}

// startDiscovery publishes the LAN name, if the station is currently letting
// the other machines in at all. A failure here is logged and tolerated, never
// fatal: the app is still perfectly reachable by IP address, and refusing to
// serve the station because a name could not be advertised would be a worse
// outcome than the inconvenience it prevents.
//
// Starting with LAN access off deliberately publishes nothing. Advertising a
// name that every machine but this one will be refused at would send people
// looking for a fault instead of for the switch.
func (a *App) startDiscovery() {
	lanAccess := a.store.Bool(settings.LANAccess)
	if !lanAccess {
		a.log.Info("network access is set to this machine only; not advertising a LAN name")
	}
	if err := a.discovery.SetPublished(lanAccess); err != nil {
		a.log.Warn("could not advertise over mDNS; reach the server by IP address instead",
			"hostname", a.cfg.MDNS.Hostname, "error", err)
	}
}

// Stop drains in-flight requests, then closes the database. Requests are
// allowed to finish rather than being cut off: a half-written response to a
// payment entry is exactly the ambiguity this system exists to remove.
func (a *App) Stop(ctx context.Context) error {
	a.log.Info("shutting down")

	// Withdraw the LAN name first: for the rest of the shutdown the station
	// should be sent to a server that is still answering, not to this one.
	if err := a.discovery.Close(); err != nil {
		a.log.Warn("could not withdraw the mDNS records", "error", err)
	}

	// Stop the nightly job before draining requests. A backup interrupted
	// part-way leaves a file that would fail its own verification, and waiting
	// out an in-flight VACUUM INTO is far cheaper than explaining a folder with
	// a corrupt backup in it.
	a.backups.Stop()
	a.ledgerChecks.Stop()

	ctx, cancel := context.WithTimeout(ctx, shutdownGrace)
	defer cancel()

	// The redirect first, and without waiting on it: it holds nothing in
	// flight worth draining, and leaving port 80 bound would stop the next
	// start of the service from taking it back.
	if a.shortSrv != nil {
		if err := a.shortSrv.Close(); err != nil {
			a.log.Warn("could not close the short address listener", "error", err)
		}
	}

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
