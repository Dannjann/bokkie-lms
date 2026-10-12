//go:build darwin || linux

package service

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

// requireRoot fails with the command to retry, rather than with a permission
// error from three calls deeper.
func requireRoot(action string) error {
	if os.Geteuid() == 0 {
		return nil
	}
	return fmt.Errorf("%s requires root; re-run as: sudo %s", action, commandLine(os.Args[0], os.Args[1:]))
}

// grantDataDirAccess hands the data directory to the service account.
//
// This is the step that decides whether the service can write at all, and
// getting it wrong shows up as a service that starts and then immediately dies
// with a permission error nobody sees.
func grantDataDirAccess(log *slog.Logger, dataDir, account string) error {
	owner, err := user.Lookup(account)
	if err != nil {
		return fmt.Errorf("look up the %s account after creating it: %w", account, err)
	}
	uid, err := strconv.Atoi(owner.Uid)
	if err != nil {
		return fmt.Errorf("account %s has a non-numeric uid %q: %w", account, owner.Uid, err)
	}
	gid, err := strconv.Atoi(owner.Gid)
	if err != nil {
		return fmt.Errorf("account %s has a non-numeric gid %q: %w", account, owner.Gid, err)
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dataDir, err)
	}
	if err := chownTree(dataDir, uid, gid); err != nil {
		return err
	}

	log.Info("data directory handed to the service account", "data_dir", dataDir, "account", account, "uid", uid, "gid", gid)
	return nil
}

func chownTree(root string, uid, gid int) error {
	return filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := os.Lchown(path, uid, gid); err != nil {
			return fmt.Errorf("give %s to uid %d: %w", path, uid, err)
		}
		return nil
	})
}
