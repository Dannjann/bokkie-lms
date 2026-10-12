package service

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	ksvc "github.com/kardianos/service"
)

// The service-control half of this package: what `update` needs in order to
// take the service down, put a different binary under it, and bring it back.
//
// These are separate from Install deliberately. Install is a one-off that
// creates accounts and firewall rules; these three are the ordinary lifecycle,
// and an update must be able to use them without any risk of re-running the
// parts that are not idempotent.

// IsInstalled reports whether a bokkie service is registered on this machine.
// An update on a host where someone runs bokkie by hand is still a legitimate
// thing to do — it just has no service to stop.
func IsInstalled(opts Options) (bool, error) {
	svc, err := newService(&program{}, opts, "", "")
	if err != nil {
		return false, err
	}
	if _, err := svc.Status(); errors.Is(err, ksvc.ErrNotInstalled) {
		return false, nil
	}
	return true, nil
}

// Stop stops the service and waits for the service manager to say so.
func Stop(log *slog.Logger, opts Options) error {
	svc, err := newService(&program{log: log}, opts, "", "")
	if err != nil {
		return err
	}
	if err := svc.Stop(); err != nil {
		return fmt.Errorf("stop the %s service (does it need administrator rights?): %w", Name, err)
	}
	log.Info("service stopped", "name", Name)
	return nil
}

// Start starts the service. It does not wait for the app to be serving —
// WaitUntilHealthy is the step that decides whether it actually came up.
func Start(log *slog.Logger, opts Options) error {
	svc, err := newService(&program{log: log}, opts, "", "")
	if err != nil {
		return err
	}
	if err := svc.Start(); err != nil {
		return fmt.Errorf("start the %s service (does it need administrator rights?): %w", Name, err)
	}
	log.Info("service started", "name", Name)
	return nil
}

// InstalledPath is where the service manager expects to find the binary, and
// therefore the only file an update may replace.
func InstalledPath() (string, error) { return installPath() }

// Swap puts source at the installed path and returns the function that undoes
// it.
//
// Handing back a restore closure rather than a path is what keeps the rollback
// honest: the caller cannot forget which file to put back, and cannot get the
// order wrong on Windows, where the sequence is three renames rather than a
// copy.
func Swap(log *slog.Logger, source string) (restore func() error, err error) {
	target, err := installPath()
	if err != nil {
		return nil, err
	}

	displaced, err := replaceFile(target, source)
	if err != nil {
		return nil, fmt.Errorf("put %s in place at %s: %w", source, target, err)
	}
	log.Info("installed binary replaced", "executable", target, "previous", displaced)

	return func() error {
		if displaced == "" {
			// There was nothing here before, so undoing means leaving nothing.
			return os.Remove(target)
		}
		// replaceFile again rather than a bare rename: it is the same dance in
		// the other direction, and on Windows the binary being put back may
		// still be the one this very process is running from.
		if _, err := replaceFile(target, displaced); err != nil {
			return fmt.Errorf("put the previous binary back at %s: %w", target, err)
		}
		if err := os.Remove(displaced); err != nil {
			log.Debug("could not remove the rejected binary", "path", displaced, "error", err)
		}
		log.Info("previous binary restored", "executable", target)
		return nil
	}, nil
}

// Discard drops the displaced binary once a new one has proved itself. It is
// best-effort by design: on Windows the old file stays locked until whatever is
// running it exits, and a leftover is harmless — the next swap overwrites it.
func Discard(log *slog.Logger) {
	target, err := installPath()
	if err != nil {
		return
	}
	if err := os.Remove(target + displacedSuffix); err != nil && !os.IsNotExist(err) {
		log.Debug("could not remove the previous binary", "path", target+displacedSuffix, "error", err)
	}
}
