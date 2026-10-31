package advance

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func mustCorrect(t *testing.T, svc *Service, database *sql.DB, id int64, d Draft, by Actor) (Voucher, error) {
	t.Helper()
	var v Voucher
	err := inTx(t, database, func(tx *sql.Tx) error {
		var err error
		v, err = svc.CorrectTx(context.Background(), tx, id, d, by)
		return err
	})
	return v, err
}

// The client's edit: the slip reads differently afterwards, its Ref. No. does
// not change, and the ledger holds the original, its reversal and the new
// lines rather than a quietly rewritten row.
func TestAnEditKeepsTheRefNoAndMovesOnlyTheDifference(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	original := mustCreate(t, svc, database, draftOf(t, 1, 500, 2, 700))
	posted := mustPost(t, svc, database, original.ID, dan)

	edited, err := mustCorrect(t, svc, database, original.ID, draftOf(t, 1, 450, 2, 700), dan)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if edited.RefNo != posted.RefNo || edited.Revision != 2 || edited.Status != StatusPosted {
		t.Errorf("the edit is Ref. No. %d rev %d, %s; want %d rev 2, posted",
			edited.RefNo, edited.Revision, edited.Status, posted.RefNo)
	}
	if got := balanceOf(t, entries, 1).Principal.Centavos(); got != 45000 {
		t.Errorf("customer 1 owes %d centavos after the edit, want 45000", got)
	}
	if got := balanceOf(t, entries, 2).Principal.Centavos(); got != 70000 {
		t.Errorf("customer 2 owes %d centavos after the edit, want 70000", got)
	}

	old, err := svc.ByID(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != StatusVoided || old.SupersededBy != edited.ID || old.VoidReason != "Edited by dan" {
		t.Errorf("the original is %s, superseded by %d, because %q", old.Status, old.SupersededBy, old.VoidReason)
	}

	// Edited again: the number holds and the revision counts on.
	again, err := mustCorrect(t, svc, database, edited.ID, draftOf(t, 1, 400, 2, 700), dan)
	if err != nil {
		t.Fatalf("second edit: %v", err)
	}
	if again.RefNo != posted.RefNo || again.Revision != 3 {
		t.Errorf("the second edit is Ref. No. %d rev %d, want %d rev 3", again.RefNo, again.Revision, posted.RefNo)
	}

	// The list shows the slip once, as it now reads.
	page, err := svc.List(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Vouchers) != 1 || page.Vouchers[0].ID != again.ID {
		t.Errorf("the list holds %+v, want only the latest revision", page.Vouchers)
	}

	// A fresh slip still takes the next number.
	next := mustPost(t, svc, database, mustCreate(t, svc, database, draftOf(t, 1, 100)).ID, dan)
	if next.RefNo != posted.RefNo+1 {
		t.Errorf("the next slip is %d, want %d", next.RefNo, posted.RefNo+1)
	}
}

// An edit undoes nothing it should not: an older revision, a draft, and a
// failed edit all leave the record as it was.
func TestWhatAnEditRefuses(t *testing.T) {
	svc, entries, database := openTestService(t)

	v := mustCreate(t, svc, database, draftOf(t, 1, 500))
	mustPost(t, svc, database, v.ID, dan)
	edited, err := mustCorrect(t, svc, database, v.ID, draftOf(t, 1, 300), dan)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := mustCorrect(t, svc, database, v.ID, draftOf(t, 1, 999), dan); !errors.Is(err, ErrNotEditable) {
		t.Errorf("editing the superseded revision: %v, want ErrNotEditable", err)
	}
	draft := mustCreate(t, svc, database, draftOf(t, 1, 100))
	if _, err := mustCorrect(t, svc, database, draft.ID, draftOf(t, 1, 999), dan); !errors.Is(err, ErrNotEditable) {
		t.Errorf("editing a draft as if posted: %v, want ErrNotEditable", err)
	}
	if _, err := mustCorrect(t, svc, database, edited.ID, Draft{Date: edited.Date}, dan); err == nil {
		t.Error("an edit with no lines was accepted")
	}
	if got := balanceOf(t, entries, 1).Principal.Centavos(); got != 30000 {
		t.Errorf("after the refusals customer 1 owes %d centavos, want 30000", got)
	}
}
