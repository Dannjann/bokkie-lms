//go:build darwin

package service

import (
	"fmt"
	"log/slog"
)

// socketfilterfw is the command line to the macOS application firewall.
const socketfilterfw = "/usr/libexec/ApplicationFirewall/socketfilterfw"

// ensureFirewall allows incoming connections to the binary.
//
// macOS filters by application rather than by port, so there is nothing to open
// for a port number — the executable itself is what gets allowed. **The first
// launch may still raise a prompt**, which is worth warning whoever installs
// this about: a dialog waiting for a click on an unattended machine looks
// exactly like a service that failed to start.
func ensureFirewall(log *slog.Logger, executable string, port int) error {
	if !fileIsExecutable(socketfilterfw) {
		return fmt.Errorf("the application firewall tool is missing at %s", socketfilterfw)
	}

	if err := runCommand(log, socketfilterfw, "--add", executable); err != nil {
		return err
	}
	if err := runCommand(log, socketfilterfw, "--unblockapp", executable); err != nil {
		return err
	}

	log.Info("application firewall allows incoming connections", "executable", executable, "port", port)
	log.Warn("macOS may still prompt once on first launch to allow incoming connections; approve it at the host machine")
	return nil
}

func removeFirewall(log *slog.Logger, _ int) error {
	executable, err := resolveExecutable()
	if err != nil {
		return err
	}
	return runCommand(log, socketfilterfw, "--remove", executable)
}
