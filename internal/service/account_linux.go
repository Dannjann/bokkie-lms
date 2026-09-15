//go:build linux

package service

import (
	"fmt"
	"log/slog"
	"os/user"
)

// accountName is the dedicated system account the service runs as.
const accountName = "bokkie"

// ensureServiceAccount creates the account if it is missing, and is safe to run
// again on a machine that already has it.
//
// Install creates the account itself rather than asking whoever is installing
// to create one first: a manual prerequisite is exactly what gets fumbled on a
// support call. **It never falls back to root.** A financial database owned by
// root because account creation quietly failed is worse than an install that
// refuses to finish.
func ensureServiceAccount(log *slog.Logger) (string, error) {
	if _, err := user.Lookup(accountName); err == nil {
		log.Info("service account already exists", "account", accountName)
		return accountName, nil
	}
	if err := requireRoot("creating the " + accountName + " account"); err != nil {
		return "", err
	}

	// --system keeps it out of the login range, and no home directory is
	// created because this account owns exactly one thing: the data directory.
	args := []string{
		"--system",
		"--no-create-home",
		"--shell", nologinShell(),
		"--comment", DisplayName + " service",
		accountName,
	}
	if err := runCommand(log, "useradd", args...); err != nil {
		return "", fmt.Errorf("create the %s account — run this by hand and try again: %w",
			accountName, err)
	}

	log.Info("service account created", "account", accountName)
	return accountName, nil
}

// nologinShell picks whichever path this distribution uses; they disagree, and
// pointing at one that does not exist leaves an account that cannot be locked.
func nologinShell() string {
	for _, candidate := range []string{"/usr/sbin/nologin", "/sbin/nologin", "/usr/bin/nologin"} {
		if fileIsExecutable(candidate) {
			return candidate
		}
	}
	return "/bin/false"
}
