package payment

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// The tests in this file go around the package entirely and write SQL by hand.
//
// That is the point. Go enforces all of this too, but the realistic way a
// financial record gets edited is not through the application — it is somebody
// opening bokkie.db in a SQLite browser because they were sure about what the
// number should have been.

// postedPayment returns the id of a payment that has been posted for real, so
// the raw-SQL attempts below run against a row in the state they are about.
func postedPayment(t *testing.T) (*sql.DB, int64) {
	t.Helper()

	svc, entries, database := openTestService(t)
	owe(t, entries, 1, pesos(t, 5000), money.Zero)
	p := mustPost(t, svc, database, mustCreate(t, svc, database, draftOf(t, 1, 1000)).ID, dan)
	return database, p.ID
}

func TestTheDatabaseRefusesToEditAPostedPayment(t *testing.T) {
	database, id := postedPayment(t)

	attempts := []struct {
		name string
		sql  string
	}{
		{"change the amount", `UPDATE payments SET amount_cents = 1 WHERE id = ?`},
		{"change the customer", `UPDATE payments SET customer_id = 2 WHERE id = ?`},
		{"change the date", `UPDATE payments SET payment_date = '2026-01-01' WHERE id = ?`},
		{"change the method", `UPDATE payments SET method = 'palay' WHERE id = ?`},
		{"change the particular", `UPDATE payments SET particular = 'something else' WHERE id = ?`},
		{"take the OR No. away", `UPDATE payments SET or_no = NULL WHERE id = ?`},
		{"renumber it", `UPDATE payments SET or_no = 999 WHERE id = ?`},
		{"delete it", `DELETE FROM payments WHERE id = ?`},
	}

	for _, tc := range attempts {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := database.Exec(tc.sql, id); err == nil {
				t.Error("the database allowed it")
			}
		})
	}
}

// A payment goes draft → posted → voided and no other way. There is no un-post
// and no re-open: correcting a posted payment means voiding it and entering a
// new one.
func TestTheDatabaseRefusesAStatusThatGoesBackwards(t *testing.T) {
	database, id := postedPayment(t)

	if _, err := database.Exec(`UPDATE payments SET status = 'draft', or_no = NULL, posted_at = NULL,
	                              posted_by_username = '' WHERE id = ?`, id); err == nil {
		t.Error("a posted payment was un-posted")
	}

	// And from voided, nowhere at all.
	if _, err := database.Exec(`UPDATE payments SET status = 'voided', voided_at = '2026-03-02T00:00:00Z',
	                              voided_by_username = 'manager', void_reason = 'x' WHERE id = ?`, id); err != nil {
		t.Fatalf("void by hand: %v", err)
	}
	for _, status := range []string{"draft", "posted"} {
		if _, err := database.Exec(`UPDATE payments SET status = ? WHERE id = ?`, status, id); err == nil {
			t.Errorf("a voided payment was moved to %s", status)
		}
	}
}

// Posting and voiding record what happened; they do not get to change what the
// receipt says. This is the trigger that catches an edit smuggled in alongside
// a legitimate status change.
func TestTheDatabaseRefusesAnEditSmuggledInWithAStatusChange(t *testing.T) {
	svc, _, database := openTestService(t)
	id := mustCreate(t, svc, database, draftOf(t, 1, 1000)).ID

	_, err := database.Exec(`UPDATE payments
	    SET status = 'posted', or_no = 1, posted_at = '2026-03-02T00:00:00Z', posted_by_username = 'dan',
	        amount_cents = 999999
	  WHERE id = ?`, id)
	if err == nil {
		t.Fatal("a payment was posted for an amount other than the one it was typed for")
	}
	if !strings.Contains(err.Error(), "may not change what it says") {
		t.Errorf("the refusal reads %q", err)
	}
}

// Each status implies exactly what it implies. A draft holding an OR No., or a
// posted receipt without one, is a row no screen could render honestly.
func TestTheDatabaseRefusesAStatusThatContradictsItsColumns(t *testing.T) {
	svc, _, database := openTestService(t)
	id := mustCreate(t, svc, database, draftOf(t, 1, 1000)).ID

	attempts := []struct {
		name string
		sql  string
	}{
		{"a numbered draft", `UPDATE payments SET or_no = 5 WHERE id = ?`},
		{"a posted payment with no number", `UPDATE payments SET status = 'posted',
		     posted_at = '2026-03-02T00:00:00Z', posted_by_username = 'dan' WHERE id = ?`},
		{"a voided payment with no reason", `UPDATE payments SET status = 'voided',
		     voided_at = '2026-03-02T00:00:00Z', voided_by_username = 'manager' WHERE id = ?`},
	}

	for _, tc := range attempts {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := database.Exec(tc.sql, id); err == nil {
				t.Error("the database allowed it")
			}
		})
	}
}

// The partial unique index is the backstop behind allocation inside the posting
// UPDATE. Two receipts cannot share a number even if the allocator is wrong.
func TestTheDatabaseRefusesADuplicateOrNo(t *testing.T) {
	svc, entries, database := openTestService(t)

	owe(t, entries, 1, pesos(t, 50000), money.Zero)
	first := mustPost(t, svc, database, mustCreate(t, svc, database, draftOf(t, 1, 500)).ID, dan)
	second := mustCreate(t, svc, database, draftOf(t, 1, 500)).ID

	_, err := database.Exec(`UPDATE payments SET status = 'posted', or_no = ?,
	     posted_at = '2026-03-02T00:00:00Z', posted_by_username = 'dan' WHERE id = ?`, first.OrNo, second)
	if err == nil {
		t.Fatalf("two receipts were given OR No. %d", first.OrNo)
	}

	// Drafts carrying NULL are simply not in the index, however many there are.
	for i := 0; i < 3; i++ {
		mustCreate(t, svc, database, draftOf(t, 1, 500))
	}
}

func TestTheDatabaseRefusesAnImpossiblePayment(t *testing.T) {
	svc, _, database := openTestService(t)
	id := mustCreate(t, svc, database, draftOf(t, 1, 1000)).ID

	attempts := []struct {
		name string
		sql  string
	}{
		{"an amount of nothing", `UPDATE payments SET amount_cents = 0 WHERE id = ?`},
		{"a negative amount", `UPDATE payments SET amount_cents = -1 WHERE id = ?`},
		{"a method nobody pays in", `UPDATE payments SET method = 'cheque' WHERE id = ?`},
		{"a date that is not one", `UPDATE payments SET payment_date = 'yesterday' WHERE id = ?`},
		{"a status that does not exist", `UPDATE payments SET status = 'pending' WHERE id = ?`},
	}

	for _, tc := range attempts {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := database.Exec(tc.sql, id); err == nil {
				t.Error("the database allowed it")
			}
		})
	}
}

// A draft is the one thing here that may be deleted outright: it moved no
// money, and what it said survives in the audit entry the route writes.
func TestTheDatabaseAllowsADraftToBeDeleted(t *testing.T) {
	svc, _, database := openTestService(t)
	id := mustCreate(t, svc, database, draftOf(t, 1, 1000)).ID

	if _, err := database.Exec(`DELETE FROM payments WHERE id = ?`, id); err != nil {
		t.Errorf("deleting a draft was refused: %v", err)
	}
}
