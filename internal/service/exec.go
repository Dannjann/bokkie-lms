package service

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// runCommand runs a system command and folds its output into the error.
//
// "exit status 1" on its own tells whoever is installing this nothing, and they
// are usually on a phone call at the time. The command line is included too, so
// the error doubles as the instruction for running it by hand.
func runCommand(log *slog.Logger, name string, args ...string) error {
	log.Debug("running command", "command", commandLine(name, args))

	output, err := exec.Command(name, args...).CombinedOutput()
	if err == nil {
		return nil
	}

	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", commandLine(name, args), err)
	}
	return fmt.Errorf("%s: %w: %s", commandLine(name, args), err, detail)
}

// commandExists reports whether a command is on PATH, for the places where the
// right tool depends on the distribution.
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func commandLine(name string, args []string) string {
	return strings.TrimSpace(name + " " + strings.Join(args, " "))
}
