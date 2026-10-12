package interest

import (
	"database/sql"
	"strings"
	"testing"
)

// The tests in this file go around the package entirely and write SQL by hand.
//
// That is the point. Go enforces all of this too, but the realistic way a
// financial record gets edited is not through the application — it is somebody
// opening bokkie.db in a SQLite browser because they were sure about what the
// number should have been. On this table that mistake is three hundred and
// sixty-four balances rather than one.

// postedRun returns a run that has been posted for real, so the raw-SQL
// attempts below run against a row in the state they are about.
func postedRun(t *testing.T) (*sql.DB, int64) {
	t.Helper()

	svc, entries, database := openTestService(t)
	lend(t, entries, 1, pesos(t, 5000))
	run := mustPost(t, svc, database, mustCreate(t, svc, database, september()).ID, manager)
	return database, run.ID
}

func TestTheDatabaseRefusesToEditAPostedRun(t *testing.T) {
	database, id := postedRun(t)

	attempts := []struct {
		name string
		sql  string
	}{
		{"change the rate", `UPDATE interest_runs SET rate_bps = 5000 WHERE id = ?`},
		{"change the method", `UPDATE interest_runs SET method = 'flat-on-advance' WHERE id = ?`},
		{"change the basis", `UPDATE interest_runs SET basis = 'principal-and-interest' WHERE id = ?`},
		{"move the period", `UPDATE interest_runs SET period_from = '2025-08-01' WHERE id = ?`},
		{"change the notes", `UPDATE interest_runs SET notes = 'something else' WHERE id = ?`},
		{"take the number away", `UPDATE interest_runs SET run_no = NULL WHERE id = ?`},
		{"renumber it", `UPDATE interest_runs SET run_no = 999 WHERE id = ?`},
		{"delete it", `DELETE FROM interest_runs WHERE id = ?`},
	}

	for _, tc := range attempts {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := database.Exec(tc.sql, id); err == nil {
				t.Error("the database allowed it")
			}
		})
	}
}

func TestTheDatabaseRefusesToEditThelinesOfAPostedRun(t *testing.T) {
	database, id := postedRun(t)

	var lineID int64
	if err := database.QueryRow(
		`SELECT id FROM interest_run_lines WHERE run_id = ?`, id).Scan(&lineID); err != nil {
		t.Fatalf("find a line: %v", err)
	}

	attempts := []struct {
		name string
		sql  string
		args []any
	}{
		{"change a charge", `UPDATE interest_run_lines SET charge_cents = 1 WHERE id = ?`, []any{lineID}},
		{"change the base it came from", `UPDATE interest_run_lines SET base_cents = 1 WHERE id = ?`, []any{lineID}},
		{"delete a line", `DELETE FROM interest_run_lines WHERE id = ?`, []any{lineID}},
		{"add a farmer to it",
			`INSERT INTO interest_run_lines (run_id, customer_id, base_cents, charge_cents) VALUES (?, 3, 100, 3)`,
			[]any{id}},
	}

	for _, tc := range attempts {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := database.Exec(tc.sql, tc.args...); err == nil {
				t.Error("the database allowed it")
			}
		})
	}
}

// A run goes draft → posted → voided and no other way.
func TestTheDatabaseRefusesAStatusThatGoesBackwards(t *testing.T) {
	database, id := postedRun(t)

	if _, err := database.Exec(`UPDATE interest_runs SET status = 'draft', run_no = NULL, posted_at = NULL,
	                              posted_by_username = '' WHERE id = ?`, id); err == nil {
		t.Error("a posted run was un-posted")
	}

	if _, err := database.Exec(`UPDATE interest_runs SET status = 'voided', voided_at = '2026-03-02T00:00:00Z',
	                              voided_by_username = 'manager', void_reason = 'x' WHERE id = ?`, id); err != nil {
		t.Fatalf("void by hand: %v", err)
	}
	if _, err := database.Exec(`UPDATE interest_runs SET status = 'posted' WHERE id = ?`, id); err == nil {
		t.Error("a voided run was re-opened")
	}
}

// The other half of the double-charge guard: the index, tested on its own by
// writing a second posted run over the same period directly.
func TestTheDatabaseRefusesASecondPostedRunOverTheSamePeriod(t *testing.T) {
	database, _ := postedRun(t)

	_, err := database.Exec(
		`INSERT INTO interest_runs
		        (run_no, period_from, period_to, method, rate_bps, basis, status,
		         created_at, created_by_username, posted_at, posted_by_username)
		 VALUES (99, '2025-09-01', '2025-09-30', 'monthly-on-balance', 300, 'principal', 'posted',
		         '2026-03-02T00:00:00Z', 'manager', '2026-03-02T00:00:00Z', 'manager')`)
	if err == nil {
		t.Fatal("the database allowed the same period to be charged twice")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Errorf("refused with %v; expected the unique index to be what refused", err)
	}

	// A *draft* over the same period is fine, because the index is partial on
	// posted. Two managers may each be looking at what September would charge.
	if _, err := database.Exec(
		`INSERT INTO interest_runs
		        (period_from, period_to, method, rate_bps, basis, status, created_at, created_by_username)
		 VALUES ('2025-09-01', '2025-09-30', 'monthly-on-balance', 300, 'principal', 'draft',
		         '2026-03-02T00:00:00Z', 'manager')`); err != nil {
		t.Errorf("a second draft over the same period was refused: %v", err)
	}
}

func TestTheDatabaseRefusesARowThatMakesNoSense(t *testing.T) {
	_, _, database := openTestService(t)

	attempts := []struct {
		name string
		sql  string
	}{
		{"a period that runs backwards", `
			INSERT INTO interest_runs (period_from, period_to, method, rate_bps, basis, created_at, created_by_username)
			VALUES ('2025-09-30', '2025-09-01', 'monthly-on-balance', 300, 'principal', '2026-03-02T00:00:00Z', 'dan')`},
		{"a rate of zero", `
			INSERT INTO interest_runs (period_from, period_to, method, rate_bps, basis, created_at, created_by_username)
			VALUES ('2025-09-01', '2025-09-30', 'monthly-on-balance', 0, 'principal', '2026-03-02T00:00:00Z', 'dan')`},
		{"a method of none", `
			INSERT INTO interest_runs (period_from, period_to, method, rate_bps, basis, created_at, created_by_username)
			VALUES ('2025-09-01', '2025-09-30', 'none', 300, 'principal', '2026-03-02T00:00:00Z', 'dan')`},
		{"a basis nobody defined", `
			INSERT INTO interest_runs (period_from, period_to, method, rate_bps, basis, created_at, created_by_username)
			VALUES ('2025-09-01', '2025-09-30', 'monthly-on-balance', 300, 'everything', '2026-03-02T00:00:00Z', 'dan')`},
		{"a draft carrying a number", `
			INSERT INTO interest_runs (run_no, period_from, period_to, method, rate_bps, basis, created_at, created_by_username)
			VALUES (7, '2025-09-01', '2025-09-30', 'monthly-on-balance', 300, 'principal', '2026-03-02T00:00:00Z', 'dan')`},
		{"nobody at all having made it", `
			INSERT INTO interest_runs (period_from, period_to, method, rate_bps, basis, created_at, created_by_username)
			VALUES ('2025-09-01', '2025-09-30', 'monthly-on-balance', 300, 'principal', '2026-03-02T00:00:00Z', '  ')`},
	}

	for _, tc := range attempts {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := database.Exec(tc.sql); err == nil {
				t.Error("the database allowed it")
			}
		})
	}
}

func TestTheDatabaseRefusesALineThatChargesNothingOrChargesTwice(t *testing.T) {
	svc, entries, database := openTestService(t)
	lend(t, entries, 1, pesos(t, 5000))
	run := mustCreate(t, svc, database, september())

	if _, err := database.Exec(
		`INSERT INTO interest_run_lines (run_id, customer_id, base_cents, charge_cents)
		 VALUES (?, 2, 100000, 0)`, run.ID); err == nil {
		t.Error("a line charging nothing was allowed")
	}
	if _, err := database.Exec(
		`INSERT INTO interest_run_lines (run_id, customer_id, base_cents, charge_cents)
		 VALUES (?, 2, 100000, -300)`, run.ID); err == nil {
		t.Error("a line charging a negative amount was allowed")
	}

	// One row per farmer per run, said by the schema rather than remembered by
	// the code.
	if _, err := database.Exec(
		`INSERT INTO interest_run_lines (run_id, customer_id, base_cents, charge_cents)
		 VALUES (?, 1, 100000, 3000)`, run.ID); err == nil {
		t.Error("the same farmer was charged twice in one run")
	}
}
