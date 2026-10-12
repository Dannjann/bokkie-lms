//go:build darwin

package service

import (
	"fmt"
	"log/slog"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
)

// accountName follows the macOS convention for daemon accounts: a leading
// underscore, hidden from the login window.
const accountName = "_bokkie"

// System accounts live below UID 500 on macOS. This range is the conventional
// home for third-party daemons and is well clear of both Apple's own accounts
// and real users.
const (
	minSystemID = 300
	maxSystemID = 499
)

// ensureServiceAccount creates the dedicated account the service runs as, and
// is safe to run again on a machine that already has it.
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

	id, err := freeSystemID()
	if err != nil {
		return "", err
	}

	steps := [][]string{
		{"dscl", ".", "-create", "/Groups/" + accountName},
		{"dscl", ".", "-create", "/Groups/" + accountName, "PrimaryGroupID", strconv.Itoa(id)},
		{"dscl", ".", "-create", "/Groups/" + accountName, "RealName", DisplayName + " service group"},
		{"dscl", ".", "-create", "/Users/" + accountName},
		{"dscl", ".", "-create", "/Users/" + accountName, "RealName", DisplayName + " service"},
		{"dscl", ".", "-create", "/Users/" + accountName, "UniqueID", strconv.Itoa(id)},
		{"dscl", ".", "-create", "/Users/" + accountName, "PrimaryGroupID", strconv.Itoa(id)},
		// No shell and no home: this account exists to own a database, and
		// nobody should ever be able to log in as it.
		{"dscl", ".", "-create", "/Users/" + accountName, "UserShell", "/usr/bin/false"},
		{"dscl", ".", "-create", "/Users/" + accountName, "NFSHomeDirectory", "/var/empty"},
		{"dscl", ".", "-create", "/Users/" + accountName, "IsHidden", "1"},
	}
	for _, step := range steps {
		if err := runCommand(log, step[0], step[1:]...); err != nil {
			return "", fmt.Errorf("create the %s account: %w", accountName, err)
		}
	}

	log.Info("service account created", "account", accountName, "uid", id)
	return accountName, nil
}

// freeSystemID finds an unused ID in the system range, used for both the user
// and its group so ownership of the data directory is unambiguous.
func freeSystemID() (int, error) {
	taken, err := assignedIDs()
	if err != nil {
		return 0, err
	}
	for id := minSystemID; id <= maxSystemID; id++ {
		if !taken[id] {
			return id, nil
		}
	}
	return 0, fmt.Errorf("no free system UID between %d and %d; create the %s account by hand", minSystemID, maxSystemID, accountName)
}

// assignedIDs reads every UID and GID the directory service knows about. Both
// are collected because the account and its group share one number.
func assignedIDs() (map[int]bool, error) {
	taken := make(map[int]bool)
	for _, listing := range [][]string{
		{".", "-list", "/Users", "UniqueID"},
		{".", "-list", "/Groups", "PrimaryGroupID"},
	} {
		output, err := exec.Command("dscl", listing...).Output()
		if err != nil {
			return nil, fmt.Errorf("list existing IDs (%s): %w", commandLine("dscl", listing), err)
		}
		for _, line := range strings.Split(string(output), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			if id, err := strconv.Atoi(fields[len(fields)-1]); err == nil {
				taken[id] = true
			}
		}
	}
	return taken, nil
}
