//go:build darwin

package service

import (
	"log/slog"
	"os/exec"
)

// prepareExecutable clears the Gatekeeper quarantine flag.
//
// A binary copied onto a Mac — from a USB stick, a download, or a file share —
// is quarantined, and refuses to run with an error that says nothing useful
// about why. Clearing it here means it is handled at install time rather than
// in a README nobody reads.
func prepareExecutable(log *slog.Logger, executable string) error {
	// The attribute is usually absent (a binary built on this machine has
	// never been quarantined), and xattr treats that as an error. There is
	// nothing to report either way, so the result is only logged.
	if err := exec.Command("xattr", "-d", "com.apple.quarantine", executable).Run(); err != nil {
		log.Debug("no Gatekeeper quarantine flag to clear", "executable", executable)
		return nil
	}

	log.Info("cleared the Gatekeeper quarantine flag", "executable", executable)
	return nil
}
