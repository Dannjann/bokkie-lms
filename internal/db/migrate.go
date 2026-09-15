package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
)

// Migrations ship inside the binary: there is no separate migrations directory
// to copy onto the host, and a binary can never be run against the wrong set.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

const migrationsDir = "migrations"

// Migrate brings the database up to the schema this binary was built with.
//
// It refuses to run against a schema *newer* than it understands. That happens
// when someone copies an older binary onto the host after an upgrade, and it is
// a data-corruption path: the old code would happily write rows that ignore
// constraints and columns it has never heard of.
func Migrate(ctx context.Context, database *sql.DB, log *slog.Logger) error {
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger()) // goose writes to stdout; we log structured
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}

	current, err := goose.EnsureDBVersionContext(ctx, database)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	latest, err := latestMigrationVersion()
	if err != nil {
		return err
	}
	if current > latest {
		return fmt.Errorf(
			"database schema is version %d but this build only understands %d: "+
				"it was written by a newer bokkie, and running an older one against it risks corrupting the ledger",
			current, latest)
	}
	if current == latest {
		log.Info("database schema is current", "version", current)
		return nil
	}

	if err := goose.UpContext(ctx, database, migrationsDir); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	log.Info("database schema migrated", "from_version", current, "to_version", latest)
	return nil
}

// latestMigrationVersion is the highest version embedded in this binary. It is
// read from the filenames rather than from goose so the check above can run
// before any migration is applied.
func latestMigrationVersion() (int64, error) {
	entries, err := migrationsFS.ReadDir(migrationsDir)
	if err != nil {
		return 0, fmt.Errorf("read embedded migrations: %w", err)
	}

	var latest int64
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || path.Ext(name) != ".sql" {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return 0, fmt.Errorf("migration %q is not named <version>_<name>.sql", name)
		}
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("migration %q has a non-numeric version: %w", name, err)
		}
		if version > latest {
			latest = version
		}
	}
	if latest == 0 {
		return 0, fmt.Errorf("no migrations are embedded in this binary")
	}
	return latest, nil
}
