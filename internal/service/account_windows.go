//go:build windows

package service

import (
	"fmt"
	"log/slog"
)

// accountName is the built-in low-privilege service account. Windows provides
// it, so unlike macOS and Linux there is nothing to create: it has no password,
// cannot log in, and has minimal rights on the machine.
//
// It is also what keeps the service running with nobody logged in — the failure
// this whole task exists to avoid.
const accountName = `NT AUTHORITY\LocalService`

func ensureServiceAccount(log *slog.Logger) (string, error) {
	log.Info("using the built-in service account", "account", accountName)
	return accountName, nil
}

// grantDataDirAccess gives LocalService control of the data directory. Windows
// inherits permissions from C:\ProgramData, which LocalService cannot write to
// by default, so without this the service starts and immediately dies trying to
// open the database.
func grantDataDirAccess(log *slog.Logger, dataDir, account string) error {
	// (OI)(CI) makes the grant inherit to the files and folders created later —
	// the database, the WAL files, the backups and the logs.
	grant := fmt.Sprintf(`%s:(OI)(CI)M`, account)
	if err := runCommand(log, "icacls", dataDir, "/grant", grant, "/T"); err != nil {
		return fmt.Errorf("give %s access to %s: %w", account, dataDir, err)
	}

	log.Info("data directory handed to the service account", "data_dir", dataDir, "account", account)
	return nil
}
