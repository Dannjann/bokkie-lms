package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Suffixes of the two temporary names replaceFile works through. They sit
// beside the target rather than in a temp directory because a rename across
// filesystems is a copy, and a copy is not atomic.
const (
	stagedSuffix    = ".new"
	displacedSuffix = ".old"
)

// replaceFile puts a copy of src at dst and returns where it moved whatever was
// at dst before, or "" if there was nothing there.
//
// The three-step dance — stage beside, move the old one aside, rename into
// place — is not caution for its own sake, it is Windows: a running executable
// cannot be overwritten, but it *can* be renamed. Moving the old file aside
// frees the name for the new one while the old process keeps running from the
// file it already has open. Unix does not need the trick, but it is harmless
// there and one code path is worth more than the few lines it saves.
//
// The caller decides what happens to the displaced file. `install` deletes it;
// `update` keeps it until the new binary has proved it can serve /healthz, and
// moves it back when it cannot.
func replaceFile(dst, src string) (displaced string, err error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
	}

	staged := dst + stagedSuffix
	if err := copyFile(staged, src); err != nil {
		return "", err
	}
	// Anything that fails from here leaves a stray .new rather than a
	// half-written binary under the real name.
	defer func() {
		if err != nil {
			os.Remove(staged)
		}
	}()

	if _, statErr := os.Stat(dst); statErr == nil {
		displaced = dst + displacedSuffix
		// A leftover .old from an earlier run would make the rename fail on
		// Windows, where renaming onto an existing name is an error.
		os.Remove(displaced)
		if err := os.Rename(dst, displaced); err != nil {
			return "", fmt.Errorf("move the existing %s aside: %w", dst, err)
		}
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("inspect %s: %w", dst, statErr)
	}

	if err := os.Rename(staged, dst); err != nil {
		// Put the old binary back rather than leaving the machine with no
		// bokkie at all under the name the service manager will look for.
		if displaced != "" {
			os.Rename(displaced, dst)
		}
		return "", fmt.Errorf("move the new %s into place: %w", dst, err)
	}
	return displaced, nil
}

// copyFile writes src to dst as an executable, replacing whatever is there.
//
// It copies the bytes rather than hardlinking or renaming, which on macOS has a
// useful side effect: extended attributes are not carried across, so the
// Gatekeeper quarantine flag on a binary that arrived by USB or download does
// not survive the copy.
func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	// Sync before the rename that follows: a crash between the two must not
	// leave a binary that exists under the right name with the wrong contents.
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(dst)
		return fmt.Errorf("flush %s: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return fmt.Errorf("close %s: %w", dst, err)
	}

	// O_CREATE only applies the mode to a file it creates, so a pre-existing
	// staged file from an interrupted run would keep whatever mode it had.
	if err := os.Chmod(dst, 0o755); err != nil {
		return fmt.Errorf("make %s executable: %w", dst, err)
	}
	return nil
}
