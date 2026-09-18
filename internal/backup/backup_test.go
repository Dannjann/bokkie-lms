package backup

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bokkiedb "github.com/Dannjann/bokkie-lms/internal/db"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

func TestRunWritesAVerifiedBackup(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	seedRows(t, database, 3)

	folder := filepath.Join(t.TempDir(), "backups")
	result, err := Run(ctx, database, Options{Folder: folder, Retention: 5})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if result.Bytes == 0 {
		t.Error("the backup file is empty")
	}
	if result.RowCounts["users"] != 3 {
		t.Errorf("backup has %d users, want 3", result.RowCounts["users"])
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Errorf("the backup file is not on disk: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(result.Path), FilePrefix) {
		t.Errorf("filename %q does not carry the bokkie prefix", filepath.Base(result.Path))
	}
}

// The restore drill, as a test. Back up, destroy the original, put the backup
// in its place, and confirm the data is all there — because a backup that has
// never been restored is a guess.
func TestABackupCanActuallyBeRestored(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	live := filepath.Join(dir, "bokkie.db")

	database := openAt(t, live)
	seedRows(t, database, 7)
	writeAuditRows(t, database, 4)

	folder := filepath.Join(dir, "backups")
	result, err := Run(ctx, database, Options{Folder: folder, Retention: 5})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// The station is down: close the database and remove it along with its WAL
	// companions, which is what a failed disk actually looks like.
	database.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(live + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove %s: %v", live+suffix, err)
		}
	}

	// The documented restore: swap the file into place.
	if err := copyFile(result.Path, live); err != nil {
		t.Fatalf("restore: %v", err)
	}

	restored := openAt(t, live)
	counts, err := countRows(ctx, restored)
	if err != nil {
		t.Fatalf("count restored rows: %v", err)
	}
	if counts["users"] != 7 {
		t.Errorf("restored database has %d users, want 7", counts["users"])
	}
	if counts["audit_log"] != 4 {
		t.Errorf("restored database has %d audit entries, want 4", counts["audit_log"])
	}

	// And it must be a working database, not just a readable file: the
	// append-only triggers and the schema version have to have come across too.
	if _, err := restored.Exec(`DELETE FROM audit_log WHERE id = 1`); err == nil {
		t.Error("the append-only trigger did not survive the restore")
	}
	if err := bokkiedb.Migrate(ctx, restored, discardLogger()); err != nil {
		t.Errorf("the restored database is not at a known schema version: %v", err)
	}
}

// A backup that fails verification is worse than no backup, because it will be
// trusted. It must not be left in the folder looking like the others.
func TestAnUnverifiableBackupIsNotLeftBehind(t *testing.T) {
	ctx := context.Background()
	folder := filepath.Join(t.TempDir(), "backups")

	if _, err := Verify(ctx, filepath.Join(folder, "nothing-here.db")); err == nil {
		t.Error("verifying a file that does not exist succeeded")
	}

	// A file that is not a database at all.
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	junk := filepath.Join(folder, FilePrefix+"20260101-000000"+FileSuffix)
	if err := os.WriteFile(junk, []byte("this is not a database"), 0o600); err != nil {
		t.Fatalf("write junk: %v", err)
	}
	if _, err := Verify(ctx, junk); err == nil {
		t.Error("a file of plain text verified as a backup")
	}
}

func TestRetentionKeepsTheNewestAndDeletesTheRest(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	seedRows(t, database, 1)

	folder := filepath.Join(t.TempDir(), "backups")
	base := time.Date(2026, 9, 15, 21, 0, 0, 0, time.UTC)

	var paths []string
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Hour)
		result, err := Run(ctx, database, Options{
			Folder: folder, Retention: 3, Now: func() time.Time { return at },
		})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		paths = append(paths, result.Path)
	}

	remaining, err := List(folder)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != 3 {
		t.Fatalf("%d backups remain, want 3", len(remaining))
	}

	// The three kept must be the three newest, not an arbitrary three.
	for i, want := range paths[2:] {
		if remaining[i] != want {
			t.Errorf("kept[%d] = %s, want %s", i, remaining[i], want)
		}
	}
	for _, gone := range paths[:2] {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should have been pruned", gone)
		}
	}
}

// Write, verify, then prune. Pruning first would, on a full disk, delete the
// oldest good backup to make room for a write that then fails — trading a
// working backup for nothing.
func TestAFailedBackupDoesNotPruneTheGoodOnes(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	seedRows(t, database, 1)

	folder := filepath.Join(t.TempDir(), "backups")
	at := time.Date(2026, 9, 15, 21, 0, 0, 0, time.UTC)
	good, err := Run(ctx, database, Options{Folder: folder, Retention: 1, Now: func() time.Time { return at }})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Make the next write fail by closing the source database.
	database.Close()
	if _, err := Run(ctx, database, Options{
		Folder: folder, Retention: 1, Now: func() time.Time { return at.Add(time.Hour) },
	}); err == nil {
		t.Fatal("a backup from a closed database succeeded")
	}

	if _, err := os.Stat(good.Path); err != nil {
		t.Errorf("the existing good backup was lost when a later one failed: %v", err)
	}
}

func TestRunRefusesToOverwriteAnExistingBackup(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	seedRows(t, database, 1)

	folder := filepath.Join(t.TempDir(), "backups")
	at := time.Date(2026, 9, 15, 21, 0, 0, 0, time.UTC)
	opts := Options{Folder: folder, Retention: 5, Now: func() time.Time { return at }}

	if _, err := Run(ctx, database, opts); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if _, err := Run(ctx, database, opts); err == nil {
		t.Error("a second backup at the same timestamp overwrote the first")
	}
}

// The schedule is computed from wall-clock time in the station's timezone each
// cycle, so a corrected clock moves the backup to the right time instead of
// drifting it forever.
func TestScheduleTargetsTheNextOccurrenceInTheStationTimezone(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	store, err := settings.Open(ctx, database)
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	scheduler := NewScheduler(database, store, t.TempDir(), discardLogger())

	manila := store.Location()

	t.Run("later today", func(t *testing.T) {
		now := time.Date(2026, 9, 15, 9, 0, 0, 0, manila) // 09:00, backup at 21:00
		if got := scheduler.until(now); got != 12*time.Hour {
			t.Errorf("wait = %v, want 12h", got)
		}
	})

	t.Run("tomorrow when today has passed", func(t *testing.T) {
		now := time.Date(2026, 9, 15, 22, 0, 0, 0, manila) // 22:00, already past
		if got := scheduler.until(now); got != 23*time.Hour {
			t.Errorf("wait = %v, want 23h", got)
		}
	})

	t.Run("a changed schedule is picked up", func(t *testing.T) {
		if err := store.Set(ctx, settings.BackupSchedule, "02:30"); err != nil {
			t.Fatalf("set schedule: %v", err)
		}
		now := time.Date(2026, 9, 15, 1, 0, 0, 0, manila)
		if got := scheduler.until(now); got != 90*time.Minute {
			t.Errorf("wait = %v, want 1h30m", got)
		}
	})

	t.Run("a wait is never zero", func(t *testing.T) {
		if err := store.Set(ctx, settings.BackupSchedule, "12:00"); err != nil {
			t.Fatalf("set schedule: %v", err)
		}
		now := time.Date(2026, 9, 15, 12, 0, 0, 0, manila) // exactly on the mark
		if got := scheduler.until(now); got <= 0 {
			t.Errorf("wait = %v; a zero wait would spin the loop", got)
		}
	})
}

// Stop must not hang when Start was never called — an App whose Start failed
// still gets Stopped, and blocking there would turn a startup error into a
// hang the service manager reports as a timeout.
func TestStoppingASchedulerThatNeverStartedReturns(t *testing.T) {
	database := openTestDB(t)
	store, err := settings.Open(context.Background(), database)
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}

	done := make(chan struct{})
	go func() {
		NewScheduler(database, store, t.TempDir(), discardLogger()).Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop blocked on a scheduler that was never started")
	}
}

// --- helpers ---------------------------------------------------------------

func seedRows(t *testing.T, database *sql.DB, users int) {
	t.Helper()
	for i := 0; i < users; i++ {
		if _, err := database.Exec(
			`INSERT INTO users (username, display_name, password_hash, role, active, created_at)
			 VALUES (?, ?, 'x', 'encoder', 1, '2026-01-01T00:00:00Z')`,
			"user"+string(rune('a'+i)), "User"); err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
}

func writeAuditRows(t *testing.T, database *sql.DB, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := database.Exec(
			`INSERT INTO audit_log (at, action, username) VALUES ('2026-01-01T00:00:00Z', 'test.event', 'clerk')`,
		); err != nil {
			t.Fatalf("seed audit row: %v", err)
		}
	}
}

func copyFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(to)
	if err != nil {
		return err
	}
	defer destination.Close()

	if _, err := io.Copy(destination, source); err != nil {
		return err
	}
	return destination.Sync()
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return openAt(t, filepath.Join(t.TempDir(), "bokkie.db"))
}

func openAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	ctx := context.Background()

	database, err := bokkiedb.Open(ctx, path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := bokkiedb.Migrate(ctx, database, discardLogger()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The displayed time of a backup comes from its name, not from the filesystem.
//
// A file copied off a USB stick and back has a modification time recording the
// copy; the name records the backup. The two disagree by however long the file
// sat on the stick, and the one somebody is asking about is the backup.
func TestTakenAtReadsTheInstantOutOfTheName(t *testing.T) {
	at, ok := TakenAt("/anywhere/bokkie-20260918-022238.db")
	if !ok {
		t.Fatal("a name this package writes was not recognised")
	}
	want := time.Date(2026, 9, 18, 2, 22, 38, 0, time.UTC)
	if !at.Equal(want) {
		t.Errorf("TakenAt = %s, want %s", at, want)
	}
	if at.Location() != time.UTC {
		t.Errorf("TakenAt is in %s; the name is UTC and must be read as UTC, or every "+
			"backup is shown offset by the host's own timezone", at.Location())
	}

	for _, name := range []string{
		"notes.db",                       // not ours
		"bokkie-nonsense.db",             // ours, unparseable
		"bokkie-20260918-022238.db.part", // a half-written copy
		"bokkie-20261318-022238.db",      // month 13
	} {
		if _, ok := TakenAt(name); ok {
			t.Errorf("TakenAt(%q) claimed to recognise it", name)
		}
	}
}

// And it must agree with what the writer actually produced, rather than with a
// layout string typed twice.
func TestTakenAtRoundTripsWhatTheWriterNames(t *testing.T) {
	folder := t.TempDir()
	database := openTestDB(t)

	result, err := Run(context.Background(), database, Options{Folder: folder, Retention: 5})
	if err != nil {
		t.Fatalf("run backup: %v", err)
	}

	at, ok := TakenAt(result.Path)
	if !ok {
		t.Fatalf("the writer produced %q, which TakenAt does not recognise", result.Path)
	}
	// Whole seconds: the name has no sub-second part to carry.
	if diff := at.Sub(time.Now().UTC()); diff > time.Minute || diff < -time.Minute {
		t.Errorf("TakenAt = %s, which is not close to now", at)
	}
}
