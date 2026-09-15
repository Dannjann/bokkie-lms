// Package service installs bokkie into the host's own service manager and runs
// it under one.
//
// The point of going through a service manager at all is that the station must
// come back up on its own: after a power cut, after a Windows update reboot,
// after a crash at 4pm on a Friday. Nobody at the buying station is going to
// log in and start a process by hand.
//
// **The Windows case is the one to be careful about.** A service that only runs
// while a user is logged in is a common and painful mistake, and it is not
// discovered until the morning the station reboots. That is why install
// registers a system service running as a dedicated account, and why the
// verification in docs/plans/01-foundation.md insists on a reboot with nobody
// logged in.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	ksvc "github.com/kardianos/service"
)

// Service identity as the host's service manager sees it.
const (
	Name        = "bokkie"
	DisplayName = "Bokkie LMS"
	Description = "Lending management for TLS Palay & Corn Buying Station."
)

// Handler is the part of the system the service manager starts and stops —
// in practice *app.App. Keeping it an interface means this package does not
// depend on the HTTP server, and the lifecycle can be tested without one.
type Handler interface {
	Start() error
	Stop(context.Context) error
}

// Options describe the install. They are separate from config.Config because
// they are decisions about *this machine* rather than about the application.
type Options struct {
	// DataDir is passed to the installed service as --data-dir, so the service
	// resolves the same directory the installer did even though it will run as
	// a different account, with a different environment.
	DataDir string
	// Port is opened in the host firewall.
	Port int
	// LogDir is where the service manager is told to put the process's stdout
	// and stderr. It must be a directory the service account can write to:
	// launchd refuses to spawn a job whose output files it cannot open, and
	// the default (/var/log) belongs to root, so the job silently never starts.
	LogDir string
}

// Run runs handler under the host's service manager, or in the foreground when
// bokkie was started from a terminal. Both paths go through here on purpose: a
// Windows service stop is not a signal, and having one lifecycle path means the
// foreground behaviour that gets tested daily is the same one the service uses.
func Run(log *slog.Logger, handler Handler, opts Options) error {
	svc, err := newService(&program{log: log, handler: handler}, opts, "", "")
	if err != nil {
		return err
	}
	return svc.Run()
}

// Install registers bokkie with the service manager and starts it.
//
// The order matters: the account has to exist before the service references it,
// the data directory has to be reachable by that account before the service
// starts, and the firewall rule is last because it is the only step that is
// merely inconvenient to have missing.
func Install(log *slog.Logger, opts Options) error {
	executable, err := resolveExecutable()
	if err != nil {
		return err
	}

	account, err := ensureServiceAccount(log)
	if err != nil {
		return err
	}
	if err := checkExecutableReachable(executable, account); err != nil {
		return err
	}
	if err := prepareExecutable(log, executable); err != nil {
		return err
	}
	if err := grantDataDirAccess(log, opts.DataDir, account); err != nil {
		return err
	}

	svc, err := newService(&program{log: log}, opts, account, executable)
	if err != nil {
		return err
	}
	if err := svc.Install(); err != nil {
		return fmt.Errorf("register the %s service (does it need administrator rights?): %w", Name, err)
	}
	log.Info("service installed", "name", Name, "account", account, "executable", executable, "data_dir", opts.DataDir)

	// A missing firewall rule is the single most likely "it doesn't work from
	// the other PC" support call — but it is fixable after the fact, so it is a
	// warning with the exact command rather than a failed install.
	if err := ensureFirewall(log, executable, opts.Port); err != nil {
		log.Warn("could not open the firewall; the app will work on this machine but may not be reachable from the others",
			"error", err)
	}

	if err := svc.Start(); err != nil {
		return fmt.Errorf("start the %s service: %w", Name, err)
	}
	log.Info("service started", "name", Name, "port", opts.Port)

	// The service manager reports "started" as soon as it has spawned the
	// process. Confirming the app actually answers is the difference between
	// an install that worked and one that will be discovered to have failed
	// the next time somebody walks up to a counter PC.
	return verifyRunning(log, opts.Port)
}

// Uninstall stops and deregisters the service.
//
// It deliberately leaves the data directory and the service account in place.
// Removing an account that owns a financial database — or the database itself —
// on the strength of an `uninstall` typed at the wrong moment is not a recovery
// anyone wants to attempt from a backup.
func Uninstall(log *slog.Logger, opts Options) error {
	svc, err := newService(&program{log: log}, opts, "", "")
	if err != nil {
		return err
	}

	if err := svc.Stop(); err != nil {
		// Already stopped, or never started: not a reason to refuse to remove it.
		log.Warn("could not stop the service before removing it", "error", err)
	}
	if err := svc.Uninstall(); err != nil {
		return fmt.Errorf("remove the %s service (does it need administrator rights?): %w", Name, err)
	}

	if err := removeFirewall(log, opts.Port); err != nil {
		log.Warn("could not remove the firewall rule; it is harmless but can be deleted by hand", "error", err)
	}

	log.Info("service removed; the data directory and the service account were left in place",
		"name", Name, "data_dir", opts.DataDir)
	return nil
}

// program adapts Handler to the service manager's callbacks. Start must return
// quickly — the service manager treats a slow start as a failed one — which is
// why app.Start returns as soon as the listener is open.
type program struct {
	log     *slog.Logger
	handler Handler
}

func (p *program) Start(ksvc.Service) error {
	if p.handler == nil {
		return fmt.Errorf("no handler to start")
	}
	return p.handler.Start()
}

func (p *program) Stop(ksvc.Service) error {
	if p.handler == nil {
		return nil
	}
	return p.handler.Stop(context.Background())
}

// newService builds the service definition. account and executable are empty
// for every operation except install, where they are what is being registered.
func newService(prog ksvc.Interface, opts Options, account, executable string) (ksvc.Service, error) {
	cfg := &ksvc.Config{
		Name:        Name,
		DisplayName: DisplayName,
		Description: Description,
		Executable:  executable,
		UserName:    account,
		// --console=false keeps the daemon's stderr quiet: the log file is the
		// record, and a service manager that captures stderr would duplicate
		// every line into a second place nobody prunes.
		Arguments: []string{"run", "--data-dir", opts.DataDir, "--console=false"},
		// systemd only: wait for the network, or mDNS starts before there is a
		// LAN address to advertise.
		Dependencies: []string{
			"After=network-online.target",
			"Wants=network-online.target",
		},
		Option: ksvc.KeyValue{
			// Not cosmetic. See Options.LogDir: the default would make launchd
			// refuse to start the job at all. bokkie's own log is the record;
			// these two files stay empty because the service runs with
			// --console=false.
			"LogDirectory":           opts.LogDir,
			"KeepAlive":              true,      // launchd: restart if it dies
			"RunAtLoad":              true,      // launchd: start at boot
			"Restart":                "always",  // systemd
			"OnFailure":              "restart", // Windows SCM recovery
			"OnFailureDelayDuration": "5s",
			"OnFailureResetPeriod":   10,
			"StartType":              "automatic",
		},
	}
	return ksvc.New(prog, cfg)
}

// resolveExecutable returns the absolute, symlink-free path of this binary.
// The service manager stores this path and uses it at every boot from now on,
// so a relative path or a symlink that someone later moves would turn into a
// service that silently stops starting.
func resolveExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find this executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", executable, err)
	}
	return resolved, nil
}
