package advance

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	bokkiedb "github.com/Dannjann/bokkie-lms/internal/db"
)

// These tests go around this package entirely and speak SQL to the file.
//
// That is the point of them. Every rule they check is enforced in Go as well,
// and Go is one careless commit from writing an UPDATE — while the realistic
// way a financial record actually gets edited is not through the application at
// all, but through somebody opening bokkie.db in a SQLite browser because they
// were sure about what the number should have been.

func TestTheDatabaseRefusesToEditAPostedVoucher(t *testing.T) {
	database := openSchema(t)
	voucher := addDraft(t, database)
	line := addLine(t, database, voucher, 1, 50000)
	post(t, database, voucher, 1425)

	for _, tc := range []struct {
		name string
		stmt string
		args []any
	}{
		{
			name: "changing the header",
			stmt: `UPDATE advance_vouchers SET notes = 'edited' WHERE id = ?`,
			args: []any{voucher},
		},
		{
			name: "changing a line's amount",
			stmt: `UPDATE advance_lines SET amount_cents = 1 WHERE id = ?`,
			args: []any{line},
		},
		{
			name: "deleting a line",
			stmt: `DELETE FROM advance_lines WHERE id = ?`,
			args: []any{line},
		},
		{
			name: "adding a line",
			stmt: `INSERT INTO advance_lines (voucher_id, customer_id, line_date, particular, amount_cents)
			       VALUES (?, 1, '2026-03-02', 'sneaked in', 10000)`,
			args: []any{voucher},
		},
		{
			name: "deleting the voucher",
			stmt: `DELETE FROM advance_vouchers WHERE id = ?`,
			args: []any{voucher},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := database.Exec(tc.stmt, tc.args...); err == nil {
				t.Error("the database allowed it")
			}
		})
	}
}

func TestTheDatabaseAllowsOnlyTheTwoTransitions(t *testing.T) {
	database := openSchema(t)

	draft := addDraft(t, database)
	if _, err := database.Exec(
		`UPDATE advance_vouchers
		    SET status = 'posted', ref_no = 1426, posted_at = '2026-03-02T09:30:00Z',
		        posted_by = 1, posted_by_username = 'dan'
		  WHERE id = ?`, draft); err != nil {
		t.Fatalf("draft → posted was refused: %v", err)
	}

	if _, err := database.Exec(
		`UPDATE advance_vouchers
		    SET status = 'voided', voided_at = '2026-03-03T09:30:00Z',
		        voided_by = 2, voided_by_username = 'manager', void_reason = 'wrong farmer'
		  WHERE id = ?`, draft); err != nil {
		t.Fatalf("posted → voided was refused: %v", err)
	}

	// And nothing else. A voided voucher going back to draft is the shape of
	// every "just fix it" that this table exists to refuse.
	for _, stmt := range []string{
		`UPDATE advance_vouchers SET status = 'draft',  ref_no = NULL, posted_at = NULL WHERE id = ?`,
		`UPDATE advance_vouchers SET status = 'posted' WHERE id = ?`,
	} {
		if _, err := database.Exec(stmt, draft); err == nil {
			t.Errorf("the database allowed %q", stmt)
		}
	}
}

func TestVoidingMayNotChangeWhatTheVoucherSays(t *testing.T) {
	database := openSchema(t)
	voucher := addDraft(t, database)
	post(t, database, voucher, 1425)

	_, err := database.Exec(
		`UPDATE advance_vouchers
		    SET status = 'voided', voided_at = '2026-03-03T09:30:00Z',
		        voided_by = 2, voided_by_username = 'manager', void_reason = 'wrong farmer',
		        voucher_date = '2020-01-01'
		  WHERE id = ?`, voucher)
	if err == nil {
		t.Error("a void was allowed to re-date the voucher it was voiding")
	}
}

func TestADraftIsEditableAndDeletable(t *testing.T) {
	database := openSchema(t)
	voucher := addDraft(t, database)
	line := addLine(t, database, voucher, 1, 50000)

	if _, err := database.Exec(`UPDATE advance_lines SET amount_cents = 60000 WHERE id = ?`, line); err != nil {
		t.Fatalf("editing a draft's line: %v", err)
	}
	if _, err := database.Exec(`UPDATE advance_vouchers SET notes = 'still typing' WHERE id = ?`, voucher); err != nil {
		t.Fatalf("editing a draft: %v", err)
	}

	// Deleting a draft takes its lines with it, which is the one case the
	// cascade is allowed to fire in.
	if _, err := database.Exec(`DELETE FROM advance_vouchers WHERE id = ?`, voucher); err != nil {
		t.Fatalf("deleting a draft: %v", err)
	}
	var lines int
	if err := database.QueryRow(`SELECT COUNT(*) FROM advance_lines WHERE voucher_id = ?`, voucher).Scan(&lines); err != nil {
		t.Fatal(err)
	}
	if lines != 0 {
		t.Errorf("deleting a draft left %d of its lines behind", lines)
	}
}

func TestTheDatabaseRefusesANonsenseVoucher(t *testing.T) {
	database := openSchema(t)
	posted := addDraft(t, database)
	post(t, database, posted, 1425)

	// The two line cases below use a draft on purpose: a posted voucher would
	// refuse them for being posted, and the rule under test would go unproven.
	draft := addDraft(t, database)

	for _, tc := range []struct {
		name string
		stmt string
		args []any
	}{
		{
			name: "two vouchers with one Ref. No.",
			stmt: `INSERT INTO advance_vouchers
			           (ref_no, voucher_date, status, created_at, created_by_username,
			            posted_at, posted_by_username)
			       VALUES (1425, '2026-03-02', 'posted', '2026-03-02T09:30:00Z', 'dan',
			               '2026-03-02T09:30:00Z', 'dan')`,
		},
		{
			name: "a draft holding a Ref. No.",
			stmt: `INSERT INTO advance_vouchers
			           (ref_no, voucher_date, status, created_at, created_by_username)
			       VALUES (1426, '2026-03-02', 'draft', '2026-03-02T09:30:00Z', 'dan')`,
		},
		{
			name: "a void with no reason",
			stmt: `INSERT INTO advance_vouchers
			           (ref_no, voucher_date, status, created_at, created_by_username,
			            posted_at, posted_by_username, voided_at, voided_by_username)
			       VALUES (1427, '2026-03-02', 'voided', '2026-03-02T09:30:00Z', 'dan',
			               '2026-03-02T09:30:00Z', 'dan', '2026-03-03T09:30:00Z', 'manager')`,
		},
		{
			name: "a line worth nothing",
			stmt: `INSERT INTO advance_lines (voucher_id, customer_id, line_date, amount_cents)
			       VALUES (?, 1, '2026-03-02', 0)`,
			args: []any{draft},
		},
		{
			name: "a line for a customer who does not exist",
			stmt: `INSERT INTO advance_lines (voucher_id, customer_id, line_date, amount_cents)
			       VALUES (?, 99, '2026-03-02', 10000)`,
			args: []any{draft},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := database.Exec(tc.stmt, tc.args...); err == nil {
				t.Error("the database allowed it")
			}
		})
	}
}

// --- helpers ---------------------------------------------------------------

// addDraft, addLine and post write vouchers straight to the file.
//
// They go around this package's service deliberately: these tests are about
// what the database refuses, and building the fixtures through the service
// would mean the setup was already subject to the rules under test.
func addDraft(t *testing.T, database *sql.DB) int64 {
	t.Helper()
	result, err := database.Exec(
		`INSERT INTO advance_vouchers (voucher_date, status, created_at, created_by, created_by_username)
		 VALUES ('2026-03-02', 'draft', '2026-03-02T09:30:00Z', 1, 'dan')`)
	if err != nil {
		t.Fatalf("add a draft: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func addLine(t *testing.T, database *sql.DB, voucherID, customerID, cents int64) int64 {
	t.Helper()
	result, err := database.Exec(
		`INSERT INTO advance_lines (voucher_id, customer_id, line_date, particular, amount_cents)
		 VALUES (?, ?, '2026-03-02', 'palay advance', ?)`, voucherID, customerID, cents)
	if err != nil {
		t.Fatalf("add a line: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// post is the allowed draft → posted transition, which is how a fixture gets
// into the state the freezing rules apply to.
func post(t *testing.T, database *sql.DB, voucherID, refNo int64) {
	t.Helper()
	_, err := database.Exec(
		`UPDATE advance_vouchers
		    SET status = 'posted', ref_no = ?, posted_at = '2026-03-02T09:30:00Z',
		        posted_by = 1, posted_by_username = 'dan'
		  WHERE id = ?`, refNo, voucherID)
	if err != nil {
		t.Fatalf("post voucher %d: %v", voucherID, err)
	}
}

func openSchema(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	database, err := bokkiedb.Open(ctx, filepath.Join(t.TempDir(), "bokkie.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := bokkiedb.Migrate(ctx, database, log); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for id := int64(1); id <= 3; id++ {
		if _, err := database.Exec(
			`INSERT INTO customers (id, code, name, created_at, updated_at)
			 VALUES (?, ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
			id, fmt.Sprintf("BA-%04d", id), fmt.Sprintf("Customer %d", id)); err != nil {
			t.Fatalf("add customer %d: %v", id, err)
		}
	}
	for _, u := range []struct {
		id   int64
		name string
		role string
	}{{1, "dan", "encoder"}, {2, "manager", "manager"}} {
		if _, err := database.Exec(
			`INSERT INTO users (id, username, display_name, password_hash, role, created_at)
			 VALUES (?, ?, ?, 'x', ?, '2026-01-01T00:00:00Z')`, u.id, u.name, u.name, u.role); err != nil {
			t.Fatalf("add user %s: %v", u.name, err)
		}
	}
	return database
}
