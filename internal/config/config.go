// Package config resolves where bokkie keeps its data and how it is configured.
//
// Two things are deliberately separate here. The *data directory* is resolved
// before anything is read from disk — it is per-OS, overridable with
// --data-dir, and is the one thing that cannot itself live in a config file.
// Everything else comes from config.yaml inside that directory, and a fresh
// install is expected to run with no config file at all.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	// The station's business dates are always rendered in Asia/Manila, and a
	// Windows host has no system tz database at all. Embedding it keeps the
	// single-binary promise: no zoneinfo to install next to the executable.
	_ "time/tzdata"

	"github.com/Dannjann/bokkie-lms/internal/logging"
	"gopkg.in/yaml.v3"
)

// FileName is the config file bokkie looks for inside the data directory.
const FileName = "config.yaml"

// dirPerm is restrictive on purpose: this tree holds the station's entire
// financial history, and on a shared office PC "readable by everyone" is not a
// defensible default.
const dirPerm fs.FileMode = 0o700

// Config is the fully resolved runtime configuration: defaults, overlaid with
// config.yaml, overlaid with command-line overrides.
type Config struct {
	// DataDir is resolved, not configured — it is where config.yaml itself was
	// read from, so it can never be set by that file.
	DataDir string `yaml:"-"`

	Port     int     `yaml:"port"`
	Timezone string  `yaml:"timezone"`
	Log      Log     `yaml:"log"`
	MDNS     MDNS    `yaml:"mdns"`
	Printer  Printer `yaml:"printer"`
	Backup   Backup  `yaml:"backup"`

	loc      *time.Location
	logLevel slog.Level
}

// Log configures the daily log files. Retention is in days, and the default is
// generous: the logs are small, and the question they answer is usually asked
// weeks after the day in question.
type Log struct {
	Level     string `yaml:"level"`
	Retention int    `yaml:"retention"`
}

// MDNS configures how the station finds the host. Hostname is the name
// published on the LAN, so the app is reached at http://<hostname>.local:<port>
// however DHCP moves the host's address around.
type MDNS struct {
	Enabled  bool   `yaml:"enabled"`
	Hostname string `yaml:"hostname"`
}

// Printer configures the dot-matrix slip path. An empty queue disables slip
// printing rather than failing at boot — most installs are set up before anyone
// has plugged the printer in.
type Printer struct {
	Queue string `yaml:"queue"`
}

// Backup configures the nightly VACUUM INTO job. Folder is usually pointed at a
// USB stick or a second disk, so it is allowed to live outside DataDir.
type Backup struct {
	Folder    string `yaml:"folder"`
	Retention int    `yaml:"retention"`
}

// Defaults returns the configuration of an install with no config.yaml.
func Defaults(dataDir string) Config {
	return Config{
		DataDir:  dataDir,
		Port:     8080,
		Timezone: "Asia/Manila",
		Log: Log{
			Level:     "info",
			Retention: 30,
		},
		MDNS: MDNS{
			Enabled:  true,
			Hostname: "bokkie",
		},
		Backup: Backup{
			Folder:    filepath.Join(dataDir, "backups"),
			Retention: 14,
		},
	}
}

// Load resolves the data directory, reads config.yaml from it if present, and
// validates the result. A missing config file is not an error; a malformed or
// invalid one is, because silently falling back to defaults would hide a typo
// in the port or the backup folder until someone noticed the wrong behaviour.
//
// dataDirOverride is the --data-dir flag, empty when it was not given.
func Load(dataDirOverride string) (Config, error) {
	dataDir, err := resolveDataDir(dataDirOverride)
	if err != nil {
		return Config{}, err
	}

	cfg := Defaults(dataDir)
	path := filepath.Join(dataDir, FileName)

	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// A fresh install. Defaults are a working configuration.
	case err != nil:
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	default:
		if err := unmarshalStrict(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", path, err)
		}
		cfg.DataDir = dataDir
	}

	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("invalid configuration in %s: %w", path, err)
	}
	return cfg, nil
}

// unmarshalStrict rejects unknown keys, so a misspelled setting is a startup
// error rather than a setting that silently never took effect.
func unmarshalStrict(data []byte, cfg *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// io.EOF here means the file is empty or all comments, which is a valid way
	// to say "use the defaults".
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (c *Config) validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port %d is out of range 1-65535", c.Port)
	}
	if c.Backup.Retention < 1 {
		return fmt.Errorf("backup.retention is %d; at least one backup must be kept", c.Backup.Retention)
	}
	if c.Log.Retention < 1 {
		return fmt.Errorf("log.retention is %d; at least one day of logs must be kept", c.Log.Retention)
	}
	level, err := logging.ParseLevel(c.Log.Level)
	if err != nil {
		return fmt.Errorf("log.level: %w", err)
	}
	c.logLevel = level

	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return fmt.Errorf("timezone %q: %w", c.Timezone, err)
	}
	c.loc = loc

	if c.MDNS.Enabled && c.MDNS.Hostname == "" {
		return fmt.Errorf("mdns.hostname is empty; set a name or turn mdns off")
	}
	if c.Backup.Folder == "" {
		c.Backup.Folder = filepath.Join(c.DataDir, "backups")
	}
	return nil
}

// Location is the timezone business dates are rendered in. Timestamps are
// stored in UTC; only presentation uses this.
func (c Config) Location() *time.Location { return c.loc }

// LogLevel is the parsed form of Log.Level, validated at load time.
func (c Config) LogLevel() slog.Level { return c.logLevel }

// LogValue renders the resolved configuration for the boot log line. There are
// no secrets in this struct today; when one arrives it must be omitted here
// rather than relying on nobody reading the log file.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("data_dir", c.DataDir),
		slog.String("database", c.DatabasePath()),
		slog.Int("port", c.Port),
		slog.String("timezone", c.Timezone),
		slog.String("log_level", c.Log.Level),
		slog.Int("log_retention_days", c.Log.Retention),
		slog.Bool("mdns_enabled", c.MDNS.Enabled),
		slog.String("mdns_hostname", c.MDNS.Hostname),
		slog.String("backup_folder", c.Backup.Folder),
		slog.Int("backup_retention", c.Backup.Retention),
		slog.String("printer_queue", c.Printer.Queue),
	)
}

// Paths derived from the data directory. They are methods rather than fields so
// there is exactly one definition of where each thing lives.
func (c Config) ConfigPath() string   { return filepath.Join(c.DataDir, FileName) }
func (c Config) DatabasePath() string { return filepath.Join(c.DataDir, "bokkie.db") }
func (c Config) LogDir() string       { return filepath.Join(c.DataDir, "logs") }
func (c Config) SlipDir() string      { return filepath.Join(c.DataDir, "slips") }

// EnsureDirs creates the data directory tree on first run. It is idempotent, so
// a deleted subdirectory heals on the next boot rather than crashing the server
// the first time something tries to write into it.
func EnsureDirs(c Config) error {
	for _, dir := range []string{c.DataDir, c.LogDir(), c.SlipDir(), c.Backup.Folder} {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}

// resolveDataDir applies the override if there is one, and otherwise asks the
// per-OS default. The override is made absolute because the service manager
// starts bokkie from a working directory nobody chose.
func resolveDataDir(override string) (string, error) {
	if override == "" {
		return defaultDataDir()
	}
	abs, err := filepath.Abs(override)
	if err != nil {
		return "", fmt.Errorf("resolve --data-dir %q: %w", override, err)
	}
	return abs, nil
}
