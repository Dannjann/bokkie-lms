package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadWithoutConfigFileUsesDefaults(t *testing.T) {
	dir := t.TempDir()

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := Defaults(dir)
	if cfg.Port != want.Port {
		t.Errorf("port = %d, want %d", cfg.Port, want.Port)
	}
	if cfg.Backup.Folder != filepath.Join(dir, "backups") {
		t.Errorf("backup folder = %q, want it inside the data dir", cfg.Backup.Folder)
	}
}

func TestLoadReadsConfigFile(t *testing.T) {
	dir := writeConfig(t, `
port: 9090
printer:
  queue: EPSON-RAW
backup:
  folder: /mnt/usb/bokkie
`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Port != 9090 {
		t.Errorf("port = %d, want 9090", cfg.Port)
	}
	if cfg.Printer.Queue != "EPSON-RAW" {
		t.Errorf("printer queue = %q, want EPSON-RAW", cfg.Printer.Queue)
	}
	if cfg.Backup.Folder != "/mnt/usb/bokkie" {
		t.Errorf("backup folder = %q, want /mnt/usb/bokkie", cfg.Backup.Folder)
	}
	if cfg.DataDir != dir {
		t.Errorf("data dir = %q, want %q — config.yaml must not be able to move it", cfg.DataDir, dir)
	}
}

// A setting that silently never took effect is worse than a refusal to start,
// so each of these is a boot-time error rather than a fallback to the default.
func TestLoadRejectsBadConfig(t *testing.T) {
	tests := map[string]string{
		"unknown key":        "prot: 9090\n",
		"port out of range":  "port: 70000\n",
		"log retention zero": "log:\n  retention: 0\n",
		// timezone and backup.retention moved to the settings table in step 2,
		// so config.yaml must now reject them rather than quietly accept a key
		// nothing reads.
		"timezone is a setting now":  "timezone: Asia/Manila\n",
		"retention is a setting now": "backup:\n  retention: 3\n",
		"malformed yaml":             "port: [\n",
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, body)); err == nil {
				t.Fatal("Load succeeded, want an error")
			}
		})
	}
}

func TestLoadMakesDataDirAbsolute(t *testing.T) {
	cfg, err := Load("relative-data-dir")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !filepath.IsAbs(cfg.DataDir) {
		t.Errorf("data dir = %q, want an absolute path — the service manager picks the working directory, not us", cfg.DataDir)
	}
}

func TestEnsureDirsCreatesTreePrivately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fresh")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := EnsureDirs(cfg); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	// Idempotent: a second boot, and a healed subdirectory, must not fail.
	if err := os.RemoveAll(cfg.LogDir()); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDirs(cfg); err != nil {
		t.Fatalf("EnsureDirs again: %v", err)
	}

	for _, path := range []string{cfg.DataDir, cfg.LogDir(), cfg.SlipDir(), cfg.Backup.Folder} {
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("missing %s: %v", path, err)
			continue
		}
		if runtime.GOOS == "windows" {
			continue // POSIX mode bits are not meaningful here.
		}
		if perm := info.Mode().Perm(); perm != fs.FileMode(0o700) {
			t.Errorf("%s has mode %o, want 700 — this tree holds the whole ledger", path, perm)
		}
	}
}

// writeConfig creates a data directory containing the given config.yaml body.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(strings.TrimLeft(body, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
