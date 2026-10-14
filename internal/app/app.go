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
	"path/filepath"
	"sync"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/api"
	"github.com/Dannjann/bokkie-lms/internal/audit"
	"github.com/Dannjann/bokkie-lms/internal/auth"
	"github.com/Dannjann/bokkie-lms/internal/backup"
	"github.com/Dannjann/bokkie-lms/internal/config"
	"github.com/Dannjann/bokkie-lms/internal/db"
	"github.com/Dannjann/bokkie-lms/internal/diagnose"
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

	// stopWanted closes when the Stop button is pressed on the station's own
	// screen. stopOnce keeps a second press from closing it twice.
	stopWanted chan struct{}
	stopOnce   sync.Once
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

	// The App is built before the handler because the handler holds it: the
	// control screens ask the running station how it is doing, and a station
	// that did not exist yet could not be asked.
	a := &App{
		cfg:          cfg,
		log:          log,
		db:           database,
		store:        store,
		discovery:    publisher,
		backups:      backups,
		ledgerChecks: ledger.NewChecker(ledgerSvc, log),
		serveErr:     make(chan error, 1),
		stopWanted:   make(chan struct{}),
	}

	handler := api.NewHandler(api.Deps{
		Station:      a,
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

	a.srv = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	return a, nil
}

// Port is where the station is listening, for the control screens.
func (a *App) Port() int { return a.cfg.Port }

// Hostname is the LAN name without ".local", for the control screens.
func (a *App) Hostname() string { return a.cfg.MDNS.Hostname }

// RequestStop asks the process to shut the station down.
//
// It signals rather than shutting down itself: whoever is running the station
// owns its lifetime — the window, a signal, a service manager — and a handler
// that called Stop directly would be tearing the server down out from under
// the request it is still answering.
func (a *App) RequestStop() { a.stopOnce.Do(func() { close(a.stopWanted) }) }

// StopWanted closes when something inside the app has asked to stop. The Stop
// button on the station's own screen is the only thing that does.
func (a *App) StopWanted() <-chan struct{} { return a.stopWanted }

// Start binds the port and begins serving in the background. It returns once
// the listener is open, so a caller that gets a nil error knows the station can
// reach the app — a service manager that reports "started" while the port is
// still in use would be lying.
func (a *App) Start() error {
	// 0.0.0.0, not localhost: the whole point is the other counter PCs and the
	// phones on the station WiFi.
	//
	// Go reads a wildcard listen address as "both families where the host
	// supports it" — 0.0.0.0 and ":" produce the same dual-stack socket — so
	// this is not the IPv4-only bind it looks like.
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
	a.backupThisSession()
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
		return
	}
	if lanAccess {
		go a.warnOnNameCollision()
	}
}

// nameCollisionDelay gives the responder time to publish and the network time
// to answer before the name is judged.
const nameCollisionDelay = 5 * time.Second

// warnOnNameCollision says so, loudly, when the LAN name resolves to a machine
// that is not this one.
//
// This is written from a real evening. A developer's own machine had a bokkie
// installed from `make install-local`, advertising the same bokkie.local on the
// same network — and the station's counter PCs were sent to it. The name
// resolved, so the browser said "refused to connect" rather than "cannot find",
// and every instinct read that as a broken install. The station's own log said
// nothing, because from where it sat everything was fine.
//
// It is a warning and never fatal. Which machine wins a contested mDNS name is
// not ours to decide, and a station that serves perfectly on its own address is
// not something to refuse to run.
func (a *App) warnOnNameCollision() {
	time.Sleep(nameCollisionDelay)

	name := a.cfg.MDNS.Hostname + ".local"
	resolved, err := net.LookupHost(name)
	if err != nil {
		// Plenty of hosts cannot resolve their own mDNS name even while every
		// other machine can. That is not a fault and must not be logged as one.
		a.log.Info("this computer cannot look up its own LAN name; the other machines may still find it",
			"name", name, "error", err)
		return
	}

	mine := diagnose.LocalIPv4()
	for _, address := range resolved {
		for _, own := range mine {
			if address == own {
				a.log.Info("the LAN name points at this computer", "name", name, "address", address)
				return
			}
		}
	}

	a.log.Warn("ANOTHER MACHINE ON THIS NETWORK IS ANSWERING TO THIS NAME",
		"name", name,
		"resolves_to", resolved,
		"this_computer", mine,
		"effect", "the counter PCs will be sent to that machine instead of this one",
		"fix", "find and stop the other bokkie, or give this one a different mdns hostname in the settings file")
}

// backupThisSession takes a backup as soon as the station is up.
//
// The nightly job assumes a machine that is always on. This one is not: it
// serves while a person has its window open, which may be eight hours a day and
// never at 2am. Without this, a station opened and closed every day would never
// back up at all.
//
// It runs in the background because nothing about serving should wait on it,
// and a failure is a warning: a missing backup is bad, and refusing to open the
// station over it is worse.
func (a *App) backupThisSession() {
	// Skipped when there is already a recent one. A station that is opened,
	// closed and opened again — a power cut, a reboot, somebody closing the
	// window by mistake — would otherwise take a backup every time, and since
	// retention counts files rather than days, a morning of restarts would
	// prune away a fortnight of history to make room for a fortnight of
	// identical copies.
	if at, _ := a.LastBackup(); !at.IsZero() && time.Since(at) < openingBackupInterval {
		a.log.Info("skipping the opening backup; a recent one already exists", "last", at)
		return
	}

	go func() {
		if _, err := a.backups.RunNow(context.Background()); err != nil {
			a.log.Warn("could not take a backup at startup", "error", err)
		}
	}()
}

// openingBackupInterval is how recent a backup has to be for the one taken on
// opening to be skipped. Half a day: a station opened each morning gets one,
// and one reopened after lunch does not.
const openingBackupInterval = 12 * time.Hour

// LastBackup is when the most recent backup was taken, and its file name.
//
// Read from the folder rather than remembered in a field: the file on disk is
// the record, and a counter in memory can disagree with it — which is exactly
// the fault step 8.5 found when a panel printed a file's modification time
// beside a name that meant something else.
func (a *App) LastBackup() (time.Time, string) {
	names, err := backup.List(a.cfg.Backup.Folder)
	if err != nil || len(names) == 0 {
		return time.Time{}, ""
	}

	var newest time.Time
	var newestName string
	for _, path := range names {
		// List returns full paths; TakenAt takes the base itself, and the
		// screen wants the file name rather than somebody's folder layout.
		at, ok := backup.TakenAt(path)
		if ok && at.After(newest) {
			newest, newestName = at, filepath.Base(path)
		}
	}
	return newest, newestName
}

// closingBackupGrace bounds the backup taken as the station closes.
//
// It is its own budget rather than the caller's: a drain is sized in seconds
// and a VACUUM INTO of a season's lending onto a USB stick is not. Bounded all
// the same, because a window that will not close is a window somebody kills.
const closingBackupGrace = 5 * time.Minute

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

	// The backup goes here: after the requests have drained, not before.
	//
	// Both halves of that matter. Before the drain it spent the caller's whole
	// shutdown deadline on a VACUUM, so Shutdown was reached with the budget
	// already gone and cut every in-flight connection — a counter PC mid-
	// payment would be severed without a response, which is exactly the
	// ambiguity this system exists to remove. And the listener was still
	// accepting throughout, so a payment posted during the VACUUM landed after
	// the snapshot and was not in the backup the screen then reported.
	//
	// It gets its own deadline rather than the caller's, which is sized for a
	// drain and not for copying a ledger onto a USB stick.
	backupCtx, cancelBackup := context.WithTimeout(context.Background(), closingBackupGrace)
	defer cancelBackup()
	if _, err := a.backups.RunNow(backupCtx); err != nil {
		a.log.Warn("could not take a backup on the way out", "error", err)
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
