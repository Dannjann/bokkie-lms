//go:build !darwin

package service

import "log/slog"

// prepareExecutable has nothing to do outside macOS: Gatekeeper quarantine is
// the only platform quirk that stops a correctly installed binary from running.
func prepareExecutable(_ *slog.Logger, _ string) error {
	return nil
}
