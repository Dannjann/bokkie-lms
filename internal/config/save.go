package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// header is written above the generated file, because the next person to open
// it will be looking for the setting they just changed and needs to know what
// kind of file this is.
const header = `# bokkie configuration.
#
# This file is rewritten by ` + "`bokkie backup -set-folder`" + `, which is the
# supported way to change where backups are written. Hand edits are honoured,
# but comments are not preserved when the file is rewritten.
#
# Settings that are business decisions rather than facts about this machine —
# when the backup runs, how many to keep — live in the app, under Settings, and
# take effect without a restart.
`

// SetBackupFolder points backups at folder and writes config.yaml.
//
// It exists so that the runbook never has to tell a non-technical owner to open
// a YAML file in a text editor. The folder stays in config.yaml rather than
// moving into the settings table for the reason given on the Backup type: it is
// a path on *this* machine, usually a USB stick, and a path stored in the
// database would be wrong the moment that database is restored onto a different
// host.
//
// The folder is written to and read back before anything is saved. A backup
// destination that turns out to be unwritable at 21:00 is discovered by nobody;
// discovering it now, while someone is standing there with the USB stick in
// their hand, is the entire point of checking.
func SetBackupFolder(dataDirOverride, folder string) (Config, error) {
	cfg, err := Load(dataDirOverride)
	if err != nil {
		return Config{}, err
	}

	absolute, err := filepath.Abs(folder)
	if err != nil {
		return Config{}, fmt.Errorf("resolve %s: %w", folder, err)
	}
	if err := checkWritable(absolute); err != nil {
		return Config{}, err
	}

	cfg.Backup.Folder = absolute
	if err := cfg.save(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func checkWritable(folder string) error {
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return fmt.Errorf("%s cannot be created — is the drive plugged in, and is the path spelled correctly?: %w", folder, err)
	}

	probe := filepath.Join(folder, ".bokkie-write-test")
	if err := os.WriteFile(probe, []byte("bokkie"), 0o600); err != nil {
		return fmt.Errorf("%s cannot be written to: %w", folder, err)
	}
	if err := os.Remove(probe); err != nil {
		return fmt.Errorf("%s was written to but the test file could not be removed: %w", folder, err)
	}
	return nil
}

// save writes config.yaml through a temporary file, so an interrupted write
// cannot leave the host with a configuration it refuses to start against.
func (c Config) save() error {
	body, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode the configuration: %w", err)
	}

	path := c.ConfigPath()
	staged := path + ".new"
	if err := os.WriteFile(staged, append([]byte(header), body...), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", staged, err)
	}
	if err := os.Rename(staged, path); err != nil {
		os.Remove(staged)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
