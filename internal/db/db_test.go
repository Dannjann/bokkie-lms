package db

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAppliesPragmas(t *testing.T) {
	ctx := context.Background()
	database := openTemp(t)

	settings := map[string]string{
		"journal_mode": "wal",
		"foreign_keys": "1",
		"busy_timeout": "5000",
		"synchronous":  "1", // NORMAL
	}
	for pragma, want := range settings {
		var got string
		if err := database.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", pragma, err)
		}
		if !strings.EqualFold(got, want) {
			t.Errorf("PRAGMA %s = %q, want %q", pragma, got, want)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	database := openTemp(t)
	log := discardLogger()

	if err := Migrate(ctx, database, log); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// A second boot must be a no-op rather than an error or a re-run.
	if err := Migrate(ctx, database, log); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var version int64
	if err := database.QueryRowContext(ctx, "SELECT MAX(version_id) FROM goose_db_version").Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	latest, err := latestMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != latest {
		t.Errorf("schema version = %d, want %d", version, latest)
	}
}

// An older binary copied onto the host after an upgrade must refuse to run,
// not write rows that ignore constraints it has never heard of.
func TestMigrateRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	database := openTemp(t)
	log := discardLogger()

	if err := Migrate(ctx, database, log); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := database.ExecContext(ctx,
		"INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, true)", 9999); err != nil {
		t.Fatalf("fake a newer schema: %v", err)
	}

	err := Migrate(ctx, database, log)
	if err == nil {
		t.Fatal("migrate succeeded against a newer schema, want a refusal")
	}
	if !strings.Contains(err.Error(), "newer bokkie") {
		t.Errorf("error = %v, want it to explain the version mismatch", err)
	}
}

func openTemp(t *testing.T) *sql.DB {
	t.Helper()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "bokkie.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
