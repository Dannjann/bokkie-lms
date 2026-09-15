package audit

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	bokkiedb "github.com/Dannjann/bokkie-lms/internal/db"
)

// Append-only is enforced by the database, not by everyone remembering. The
// realistic way an audit trail gets edited is not a careless commit — it is
// someone typing an UPDATE into a sqlite3 shell, and that is what this checks.
func TestAuditLogRefusesUpdatesAndDeletes(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	log := New(database, discardLogger())
	clerk := seedUser(t, database, "clerk")

	if err := log.Record(ctx, Entry{
		Action: "payment.posted", Entity: "payment", EntityID: "42",
		Username: "clerk", UserID: clerk,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	_, err := database.Exec(`UPDATE audit_log SET action = 'nothing.happened' WHERE id = 1`)
	if err == nil {
		t.Error("an UPDATE against audit_log succeeded")
	} else if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("UPDATE failed with %v, want the append-only message", err)
	}

	_, err = database.Exec(`DELETE FROM audit_log WHERE id = 1`)
	if err == nil {
		t.Error("a DELETE against audit_log succeeded")
	} else if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("DELETE failed with %v, want the append-only message", err)
	}

	var count int
	if err := database.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("%d entries remain, want the original 1", count)
	}
}

func TestRecordStoresBeforeAndAfterAsJSON(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	boss := seedUser(t, database, "boss")

	err := New(database, discardLogger()).Record(ctx, Entry{
		Action:   "setting.changed",
		Entity:   "setting",
		EntityID: "backup.retention",
		Before:   map[string]string{"value": "14"},
		After:    map[string]string{"value": "21"},
		Username: "boss",
		UserID:   boss,
		IP:       "192.168.1.20",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	var before, after, ip string
	var userID sql.NullInt64
	row := database.QueryRow(`SELECT before_json, after_json, ip, user_id FROM audit_log`)
	if err := row.Scan(&before, &after, &ip, &userID); err != nil {
		t.Fatalf("read back: %v", err)
	}

	if !strings.Contains(before, `"14"`) || !strings.Contains(after, `"21"`) {
		t.Errorf("before=%q after=%q, want the old and new values", before, after)
	}
	if ip != "192.168.1.20" {
		t.Errorf("ip = %q", ip)
	}
	if !userID.Valid || userID.Int64 != boss {
		t.Errorf("user_id = %+v, want %d", userID, boss)
	}
}

// A failed login has no user to attribute to. It must still be recorded, with
// the username that was tried — "someone tried to log in as admin forty times"
// is exactly the entry worth keeping.
func TestAnEntryWithNoUserStoresNullNotZero(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)

	if err := New(database, discardLogger()).Record(ctx, Entry{
		Action: ActionLoginFailed, Username: "admin",
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	var userID sql.NullInt64
	var username string
	if err := database.QueryRow(`SELECT user_id, username FROM audit_log`).Scan(&userID, &username); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if userID.Valid {
		// A literal 0 would break the foreign key and read as "user zero".
		t.Errorf("user_id = %d, want NULL for an unauthenticated event", userID.Int64)
	}
	if username != "admin" {
		t.Errorf("username = %q, want the username that was tried", username)
	}
}

// An entry and the change it describes must commit or roll back together: a
// posted voucher with no audit trail, and an audit entry for a voucher that
// never existed, are both worse than either failing outright.
func TestRecordTxRollsBackWithItsTransaction(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	log := New(database, discardLogger())

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := log.RecordTx(ctx, tx, Entry{Action: "advance.posted", Entity: "advance", EntityID: "9"}); err != nil {
		t.Fatalf("record in tx: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var count int
	if err := database.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("%d entries survived a rolled-back transaction", count)
	}
}

func TestAnEntryWithoutAnActionIsRefused(t *testing.T) {
	err := New(openTestDB(t), discardLogger()).Record(context.Background(), Entry{Entity: "payment"})
	if err == nil {
		t.Error("an entry with no action was accepted; it would be unsearchable")
	}
}

// The user_id foreign key is deliberate: an audit entry must point at a real
// account, and a user with history can therefore never be deleted — only
// deactivated. That is the correct constraint for a financial record.
func seedUser(t *testing.T, database *sql.DB, username string) int64 {
	t.Helper()

	res, err := database.Exec(
		`INSERT INTO users (username, display_name, password_hash, role, active, created_at)
		 VALUES (?, ?, 'x', 'encoder', 1, '2026-01-01T00:00:00Z')`, username, username)
	if err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	return id
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	database, err := bokkiedb.Open(ctx, filepath.Join(t.TempDir(), "bokkie.db"))
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
