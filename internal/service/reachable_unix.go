//go:build darwin || linux

package service

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// checkExecutableReachable fails if the service account cannot reach the
// binary, which is the difference between an install that works and one that
// reports success and then never starts.
//
// It is an easy mistake to make: the obvious thing to type is `sudo ./bokkie
// install` from wherever the binary was built, and on macOS a home directory is
// 0700 — the service account cannot traverse it, so launchd has nothing to run
// and there is not even a log file to explain why. The binary has to live on a
// path the whole machine can read.
func checkExecutableReachable(executable, account string) error {
	uid, err := accountUID(account)
	if err != nil {
		return err
	}

	// Every directory from the root down has to be traversable, not just the
	// one the binary sits in.
	for _, dir := range ancestors(filepath.Dir(executable)) {
		if err := checkPathMode(dir, uid, 0o001, "enter"); err != nil {
			return unreachable(executable, account, err)
		}
	}
	if err := checkPathMode(executable, uid, 0o005, "read and execute"); err != nil {
		return unreachable(executable, account, err)
	}
	return nil
}

func unreachable(executable, account string, cause error) error {
	return fmt.Errorf(
		"the %s account cannot reach %s: %w.\n"+
			"Install from a path the whole machine can read:\n"+
			"    sudo cp %q /usr/local/bin/bokkie\n"+
			"    sudo /usr/local/bin/bokkie install",
		account, executable, cause, executable)
}

// checkPathMode allows the path either because the service account owns it or
// because everyone has the required access. Group membership is deliberately
// not considered: the account is created with a group of its own, so a group
// bit that happens to be set belongs to somebody else.
func checkPathMode(path string, uid int, needed os.FileMode, action string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot inspect %s: %w", path, err)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) == uid {
		return nil
	}
	if info.Mode().Perm()&needed == needed {
		return nil
	}
	return fmt.Errorf("%s is mode %#o, so other accounts cannot %s it", path, info.Mode().Perm(), action)
}

// ancestors lists dir and every directory above it, root first.
func ancestors(dir string) []string {
	var paths []string
	for {
		paths = append([]string{dir}, paths...)
		parent := filepath.Dir(dir)
		if parent == dir {
			return paths
		}
		dir = parent
	}
}

func accountUID(account string) (int, error) {
	owner, err := user.Lookup(account)
	if err != nil {
		return 0, fmt.Errorf("look up the %s account: %w", account, err)
	}
	uid, err := strconv.Atoi(owner.Uid)
	if err != nil {
		return 0, fmt.Errorf("account %s has a non-numeric uid %q: %w", account, owner.Uid, err)
	}
	return uid, nil
}
