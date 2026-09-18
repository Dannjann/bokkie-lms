// Package backup makes and verifies copies of the database.
//
// This replaces the legacy system's "Back-up Files" menu item, and it is the
// single highest-value thing in step 2: everything else in this system can be
// rebuilt from source, and the ledger cannot.
//
// Two rules shape the whole package. First, VACUUM INTO rather than copying the
// file: a copy of a live WAL database can be torn across the .db and its -wal
// companion, producing a file that opens fine and is missing the last hour of
// payments. Second, every backup is verified by reopening it and counting rows.
// A backup that has never been verified is not a backup; it is a folder of
// files you hope are backups.
package backup

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// FilePrefix and fileTimeLayout define the backup filename. The timestamp is
// UTC and sorts lexicographically, so "newest" is the last name in the
// directory listing whatever locale the host is in.
const (
	FilePrefix     = "bokkie-"
	FileSuffix     = ".db"
	fileTimeLayout = "20060102-150405"
)

// TakenAt reads back the instant a backup file was written, from its name.
//
// The name is the only trustworthy source for this. A file's modification time
// says when those bytes last landed on that filesystem, which for a backup
// copied off a USB stick and back is the moment of the copy and not the moment
// of the backup — the same reason List sorts on the name. The second return is
// false for a name this package did not write.
func TakenAt(name string) (time.Time, bool) {
	base := filepath.Base(name)
	if !strings.HasPrefix(base, FilePrefix) || !strings.HasSuffix(base, FileSuffix) {
		return time.Time{}, false
	}
	stamp := base[len(FilePrefix) : len(base)-len(FileSuffix)]
	at, err := time.ParseInLocation(fileTimeLayout, stamp, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// verifiedTables are counted in the source and again in the backup. Any
// mismatch means the copy is not the database we meant to copy.
//
// The list grows with the schema. It is written out rather than discovered from
// sqlite_master on purpose: a table that silently stopped existing would then
// silently stop being checked, which is the exact failure this is here to
// catch.
var verifiedTables = []string{"users", "sessions", "audit_log", "settings"}

// Result is what one backup run produced.
type Result struct {
	Path      string
	Bytes     int64
	Took      time.Duration
	RowCounts map[string]int
	Pruned    []string // files deleted to honour the retention count
}

// LogValue renders a Result for the log line that follows every run.
func (r Result) LogValue() slog.Value {
	counts := make([]any, 0, len(r.RowCounts)*2)
	for _, table := range verifiedTables {
		counts = append(counts, table, r.RowCounts[table])
	}
	return slog.GroupValue(
		slog.String("path", r.Path),
		slog.Int64("bytes", r.Bytes),
		slog.Int64("took_ms", r.Took.Milliseconds()),
		slog.Int("pruned", len(r.Pruned)),
		slog.Group("rows", counts...),
	)
}

// Options configures one run.
type Options struct {
	// Folder is where backup files are written. It is usually a USB stick or a
	// second disk, so it is allowed to live outside the data directory — a
	// backup on the same disk as the original protects against a mistake but
	// not against the disk.
	Folder string

	// Retention is how many files to keep.
	Retention int

	// Now is injectable so a test can produce two backups a second apart
	// without waiting.
	Now func() time.Time
}

// Run writes one verified backup and prunes old ones.
//
// The order matters: write, verify, and only then prune. Pruning first would,
// on a full disk, delete the oldest good backup in order to make room for a
// write that then fails — trading a working backup for nothing.
func Run(ctx context.Context, database *sql.DB, opts Options) (Result, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	if opts.Retention < 1 {
		opts.Retention = 1
	}
	if opts.Folder == "" {
		return Result{}, fmt.Errorf("backup folder is not set")
	}

	started := now()
	if err := os.MkdirAll(opts.Folder, 0o700); err != nil {
		return Result{}, fmt.Errorf("create backup folder %s: %w", opts.Folder, err)
	}

	path := filepath.Join(opts.Folder, FilePrefix+started.UTC().Format(fileTimeLayout)+FileSuffix)
	if _, err := os.Stat(path); err == nil {
		return Result{}, fmt.Errorf("backup %s already exists; refusing to overwrite it", path)
	}

	want, err := countRows(ctx, database)
	if err != nil {
		return Result{}, fmt.Errorf("count rows before backup: %w", err)
	}

	// VACUUM INTO is a single statement that produces an internally consistent
	// file, taking the same snapshot a reader would see. It cannot run inside a
	// transaction, which is why the API route for it is registered as
	// non-transactional.
	if _, err := database.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return Result{}, fmt.Errorf("write backup %s: %w", path, err)
	}

	got, err := Verify(ctx, path)
	if err != nil {
		// A backup that failed verification is worse than no backup, because
		// it will be trusted. Remove it rather than leave it in the folder
		// looking like the others.
		if rmErr := os.Remove(path); rmErr != nil {
			return Result{}, fmt.Errorf("%w (and the unverified file %s could not be removed: %v)", err, path, rmErr)
		}
		return Result{}, err
	}
	if err := compareCounts(want, got); err != nil {
		if rmErr := os.Remove(path); rmErr != nil {
			return Result{}, fmt.Errorf("%w (and the bad file %s could not be removed: %v)", err, path, rmErr)
		}
		return Result{}, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return Result{}, fmt.Errorf("stat backup %s: %w", path, err)
	}

	pruned, err := prune(opts.Folder, opts.Retention)
	if err != nil {
		// The backup itself succeeded. Failing the run because housekeeping
		// failed would report "no backup" when there is a good one on disk.
		return Result{
			Path: path, Bytes: info.Size(), Took: now().Sub(started), RowCounts: got,
		}, fmt.Errorf("backup written to %s but old backups could not be pruned: %w", path, err)
	}

	return Result{
		Path:      path,
		Bytes:     info.Size(),
		Took:      now().Sub(started),
		RowCounts: got,
		Pruned:    pruned,
	}, nil
}

// Verify opens a backup file read-only and counts its rows.
//
// It is exported so the same check can be run by hand against any file in the
// backup folder — which is what someone will want to do before trusting one in
// a restore.
func Verify(ctx context.Context, path string) (map[string]int, error) {
	// mode=ro so verification can never modify what it is verifying, and
	// immutable=1 so SQLite does not try to recover a -wal alongside it. A
	// VACUUM INTO output has no WAL, and treating it as immutable keeps this
	// read from creating files next to the backup.
	dsn := "file:" + path + "?mode=ro&immutable=1"

	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("verify %s: %w", path, err)
	}
	defer database.Close()

	if err := database.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("verify %s: cannot open the backup: %w", path, err)
	}

	var check string
	if err := database.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&check); err != nil {
		return nil, fmt.Errorf("verify %s: integrity check failed to run: %w", path, err)
	}
	if !strings.EqualFold(check, "ok") {
		return nil, fmt.Errorf("verify %s: integrity check says %q", path, check)
	}

	counts, err := countRows(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("verify %s: %w", path, err)
	}
	return counts, nil
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func countRows(ctx context.Context, database querier) (map[string]int, error) {
	counts := make(map[string]int, len(verifiedTables))
	for _, table := range verifiedTables {
		var n int
		// The table names are the package-level constant list above, never
		// anything from a request, so interpolating them is safe — and SQLite
		// has no way to parameterise a table name.
		if err := database.QueryRowContext(ctx, `SELECT count(*) FROM "`+table+`"`).Scan(&n); err != nil {
			return nil, fmt.Errorf("count %s: %w", table, err)
		}
		counts[table] = n
	}
	return counts, nil
}

// compareCounts is the actual verification.
//
// sessions is excluded from the comparison: a login or a logout between the
// count and the VACUUM is entirely normal and says nothing about the backup's
// integrity. The tables that must match are the ones a mismatch would mean
// real data loss in.
func compareCounts(want, got map[string]int) error {
	for _, table := range verifiedTables {
		if table == "sessions" {
			continue
		}
		if want[table] != got[table] {
			return fmt.Errorf(
				"backup verification failed: %s has %d rows in the backup but %d in the database",
				table, got[table], want[table])
		}
	}
	return nil
}

// List returns the backup files in folder, oldest first.
func List(folder string) ([]string, error) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backup folder %s: %w", folder, err)
	}

	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, FilePrefix) || !strings.HasSuffix(name, FileSuffix) {
			continue
		}
		files = append(files, filepath.Join(folder, name))
	}
	// The timestamp in the name sorts chronologically, so this is oldest first
	// without stat-ing anything — which matters because a file copied off a USB
	// stick and back would have a useless mtime.
	sort.Strings(files)
	return files, nil
}

// prune deletes the oldest files beyond the retention count.
func prune(folder string, retention int) ([]string, error) {
	files, err := List(folder)
	if err != nil {
		return nil, err
	}
	if len(files) <= retention {
		return nil, nil
	}

	var pruned []string
	for _, path := range files[:len(files)-retention] {
		if err := os.Remove(path); err != nil {
			return pruned, fmt.Errorf("remove old backup %s: %w", path, err)
		}
		pruned = append(pruned, path)
	}
	return pruned, nil
}

// VerifiedTables is the list of tables a backup's row counts are checked
// against. Exported so the CLI can print the same set it verified, rather than
// keeping its own copy that would drift as the schema grows.
func VerifiedTables() []string {
	out := make([]string, len(verifiedTables))
	copy(out, verifiedTables)
	return out
}
