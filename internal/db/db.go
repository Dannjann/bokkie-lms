// Package db opens the SQLite database and keeps its schema current.
//
// Everything the rest of the system relies on about SQLite's behaviour —
// crash-safe writes, concurrent readers, enforced foreign keys — is decided by
// the pragmas set here, and they are verified after connecting rather than
// assumed. A pragma that silently failed to apply is the kind of fault that
// only shows up as corrupted data months later.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	// Pure-Go SQLite. This is load-bearing, not a preference: a cgo driver
	// (mattn/go-sqlite3) ends cross-compilation for every non-host target at
	// once, and with it the host-OS freedom in Decision 5 of the architecture.
	// Do not swap this import without reading docs/plans/00-architecture.md.
	_ "modernc.org/sqlite"
)

// pragmas are applied to every connection in the pool, not just the first —
// they are per-connection settings in SQLite, so passing them in the DSN is the
// only way to be sure a freshly opened pool connection has them.
var pragmas = []string{
	"journal_mode(WAL)",   // concurrent readers while a write is in flight
	"foreign_keys(ON)",    // off by default in SQLite; a real footgun
	"busy_timeout(5000)",  // wait for a lock rather than instantly failing
	"synchronous(NORMAL)", // correct and fast in WAL mode
}

// Open connects to the database at path, creating it if it does not exist.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	database, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}

	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("connect to database %s: %w", path, err)
	}
	if err := verifyPragmas(ctx, database); err != nil {
		database.Close()
		return nil, fmt.Errorf("database %s: %w", path, err)
	}
	return database, nil
}

func dsn(path string) string {
	params := make([]string, 0, len(pragmas))
	for _, p := range pragmas {
		params = append(params, "_pragma="+p)
	}
	return "file:" + path + "?" + strings.Join(params, "&")
}

// verifyPragmas reads back the settings that matter. WAL is the one that can
// legitimately fail — on a filesystem that does not support shared memory, such
// as some network mounts — and that is exactly the configuration the
// architecture forbids, so refusing to start is the right answer.
func verifyPragmas(ctx context.Context, database *sql.DB) error {
	var journalMode string
	if err := database.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return fmt.Errorf("read journal_mode: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("journal_mode is %q, want WAL — is the database on a network share?", journalMode)
	}

	var foreignKeys int
	if err := database.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("read foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("foreign_keys is off; referential integrity would not be enforced")
	}
	return nil
}
