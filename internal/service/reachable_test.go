//go:build darwin || linux

package service

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// The failure this guards against: `sudo ./bokkie install` run from a home
// directory. The install reports success, the service account cannot traverse
// the path, and there is not even a log file to explain why nothing runs.
func TestCheckExecutableReachableRejectsAPrivateDirectory(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Skipf("cannot determine the current user: %v", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: every path is reachable, so there is nothing to detect")
	}

	private := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(private, "bokkie")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Checked against the current user, who owns the directory, this passes;
	// the point of the check is another account, so use one that cannot be the
	// owner. "nobody" exists on both macOS and Linux.
	if err := checkExecutableReachable(executable, current.Username); err != nil {
		t.Errorf("the owner should be able to reach its own file: %v", err)
	}

	err = checkExecutableReachable(executable, "nobody")
	if err == nil {
		t.Fatal("a 0700 directory was accepted; the service would fail to start with no explanation")
	}
	if !strings.Contains(err.Error(), "/usr/local/bin/bokkie") {
		t.Errorf("error = %v, want it to name the fix", err)
	}
}

func TestCheckExecutableReachableAcceptsAWorldReadablePath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: every path is reachable")
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// t.TempDir sits under a private per-run directory on macOS, so check a
	// path that is world-traversable all the way up instead.
	executable := "/bin/sh"
	if err := checkExecutableReachable(executable, "nobody"); err != nil {
		t.Errorf("%s should be reachable by any account: %v", executable, err)
	}
}
