package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetBackupFolderSurvivesBeingReadBack(t *testing.T) {
	dataDir := t.TempDir()
	folder := filepath.Join(t.TempDir(), "stick", "bokkie-backups")

	saved, err := SetBackupFolder(dataDir, folder)
	if err != nil {
		t.Fatalf("SetBackupFolder: %v", err)
	}
	if saved.Backup.Folder != folder {
		t.Errorf("saved folder = %q, want %q", saved.Backup.Folder, folder)
	}

	// Load rejects unknown keys, so this also proves the file the command
	// writes is one the service will still start against.
	reloaded, err := Load(dataDir)
	if err != nil {
		t.Fatalf("reading back the file the command just wrote: %v", err)
	}
	if reloaded.Backup.Folder != folder {
		t.Errorf("reloaded folder = %q, want %q", reloaded.Backup.Folder, folder)
	}
}

func TestSetBackupFolderKeepsEverythingElse(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, FileName), []byte("port: 9999\nlog:\n  level: debug\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := SetBackupFolder(dataDir, filepath.Join(t.TempDir(), "backups")); err != nil {
		t.Fatalf("SetBackupFolder: %v", err)
	}

	reloaded, err := Load(dataDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.Port != 9999 {
		t.Errorf("port = %d, want the 9999 that was already in the file", reloaded.Port)
	}
	if reloaded.Log.Level != "debug" {
		t.Errorf("log level = %q, want the debug that was already in the file", reloaded.Log.Level)
	}
}

// The whole reason to check the destination now is that nobody is watching at
// 21:00 when the nightly backup discovers the stick is not there.
func TestSetBackupFolderRefusesADestinationItCannotWriteTo(t *testing.T) {
	dataDir := t.TempDir()

	// A file, not a directory: the closest stand-in for a mistyped path that
	// does not depend on running as an unprivileged user.
	blocked := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := SetBackupFolder(dataDir, blocked); err == nil {
		t.Fatal("SetBackupFolder accepted a path that cannot hold backups")
	}
	if _, err := os.Stat(filepath.Join(dataDir, FileName)); !os.IsNotExist(err) {
		t.Error("a configuration file was written even though the destination was refused")
	}
}
