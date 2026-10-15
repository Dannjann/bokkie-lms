package advance

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
)

func mustPost(t *testing.T, svc *Service, database *sql.DB, id int64, by Actor) Voucher {
	t.Helper()

	var v Voucher
	err := inTx(t, database, func(tx *sql.Tx) error {
		var err error
		v, err = svc.PostTx(context.Background(), tx, id, by)
		return err
	})
	if err != nil {
		t.Fatalf("post voucher %d: %v", id, err)
	}
	return v
}

func balanceOf(t *testing.T, entries *ledger.Service, customerID int64) ledger.Balance {
	t.Helper()

	b, err := entries.BalanceFor(context.Background(), customerID)
	if err != nil {
		t.Fatalf("balance for customer %d: %v", customerID, err)
	}
	return b
}

func TestPostingNumbersTheVoucherAndMovesTheMoney(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	draft := mustCreate(t, svc, database, draftOf(t, 1, 500, 2, 700))
	posted := mustPost(t, svc, database, draft.ID, dan)

	if posted.Status != StatusPosted {
		t.Errorf("the voucher is %s after posting", posted.Status)
	}
	// The default start continues the legacy sequence, whose last Ref. No. was 1425.
	if posted.RefNo != 1426 {
		t.Errorf("the first voucher was numbered %d, want 1426 — the default start", posted.RefNo)
	}
	if posted.PostedAt == nil || posted.PostedBy.Username != "dan" {
		t.Errorf("the voucher does not record who posted it and when: %v, %q", posted.PostedAt, posted.PostedBy.Username)
	}

	if got := balanceOf(t, entries, 1).Principal.Centavos(); got != 50000 {
		t.Errorf("customer 1 owes %d centavos, want 50000", got)
	}
	if got := balanceOf(t, entries, 2).Principal.Centavos(); got != 70000 {
		t.Errorf("customer 2 owes %d centavos, want 70000", got)
	}

	// One entry per line, each naming the line it came from, each dated the
	// line's own date, and none of them carrying interest.
	produced, err := svc.Entries(ctx, draft.ID)
	if err != nil {
		t.Fatalf("read what the voucher produced: %v", err)
	}
	if len(produced) != len(posted.Lines) {
		t.Fatalf("a %d-line voucher produced %d entries", len(posted.Lines), len(produced))
	}
	for i, e := range produced {
		line := posted.Lines[i]
		switch {
		case e.Type != ledger.TypeAdvance:
			t.Errorf("entry %d is a %s, want an advance", i, e.Type)
		case e.SourceTable != SourceTable || e.SourceID != line.ID:
			t.Errorf("entry %d points at %s/%d, want %s/%d", i, e.SourceTable, e.SourceID, SourceTable, line.ID)
		case e.Principal != line.Amount:
			t.Errorf("entry %d is for %s, want the line's %s", i, e.Principal, line.Amount)
		case !e.Interest.IsZero():
			t.Errorf("entry %d carries interest of %s; an advance is principal", i, e.Interest)
		case !e.Date.Equal(line.Date):
			t.Errorf("entry %d is dated %s, want the line's %s", i, e.Date, line.Date)
		}
	}
}

func TestTheRefNoSequenceContinuesTheLegacyOne(t *testing.T) {
	svc, _, database := openStationService(t, station{refNoStart: 1426})

	first := mustCreate(t, svc, database, draftOf(t, 1, 500))
	second := mustCreate(t, svc, database, draftOf(t, 2, 700))

	if got := mustPost(t, svc, database, first.ID, dan).RefNo; got != 1426 {
		t.Errorf("the first voucher was numbered %d, want the sequence to start at 1426", got)
	}
	if got := mustPost(t, svc, database, second.ID, dan).RefNo; got != 1427 {
		t.Errorf("the second voucher was numbered %d, want 1427", got)
	}

	// Moving refno.start after the sequence has begun changes nothing: MAX
	// wins, by design. A setting that could renumber a live sequence would be a
	// footgun, which is why the legacy number goes in before the first post.
	moved, _, _ := reopen(t, database, station{refNoStart: 5000})
	third := mustCreate(t, moved, database, draftOf(t, 3, 300))
	if got := mustPost(t, moved, database, third.ID, dan).RefNo; got != 1428 {
		t.Errorf("after moving refno.start to 5000 the next voucher was %d, want 1428", got)
	}
}

func TestPostingTwiceIsRefused(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	draft := mustCreate(t, svc, database, draftOf(t, 1, 500))
	mustPost(t, svc, database, draft.ID, dan)

	err := inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.PostTx(ctx, tx, draft.ID, dan)
		return err
	})
	if !errors.Is(err, ErrNotDraft) {
		t.Errorf("posting a posted voucher gave %v, want ErrNotDraft", err)
	}
	if got := balanceOf(t, entries, 1).Principal.Centavos(); got != 50000 {
		t.Errorf("customer 1 owes %d centavos after a refused second post, want 50000", got)
	}
}

func TestAPostedVoucherCannotBeEditedOrDeleted(t *testing.T) {
	svc, _, database := openTestService(t)
	ctx := context.Background()

	draft := mustCreate(t, svc, database, draftOf(t, 1, 500))
	mustPost(t, svc, database, draft.ID, dan)

	err := inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.UpdateTx(ctx, tx, draft.ID, draftOf(t, 1, 100))
		return err
	})
	if !errors.Is(err, ErrNotDraft) {
		t.Errorf("editing a posted voucher gave %v, want ErrNotDraft", err)
	}

	err = inTx(t, database, func(tx *sql.Tx) error {
		return svc.DeleteTx(ctx, tx, draft.ID)
	})
	if !errors.Is(err, ErrNotDraft) {
		t.Errorf("deleting a posted voucher gave %v, want ErrNotDraft", err)
	}
}

// TestConcurrentPostsGetDistinctRefNos is the test that matters most in this
// step, and the one to check against a deliberate break: swapping the allocator
// for a read of MAX followed by a write must make it fail. Step 4 found that
// the symptom is not a duplicate number but a storm of SQLITE_BUSY, because a
// deferred transaction cannot upgrade its lock — so a failure here may read as
// "database is locked" rather than as two vouchers sharing a number.
func TestConcurrentPostsGetDistinctRefNos(t *testing.T) {
	const posters = 20

	svc, _, database := openStationService(t, station{refNoStart: 1426})
	ctx := context.Background()

	ids := make([]int64, 0, posters)
	for i := 0; i < posters; i++ {
		ids = append(ids, mustCreate(t, svc, database, draftOf(t, int64(i%3)+1, 500)).ID)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		refNos  = make(map[int64]bool, posters)
		failure error
	)
	for _, id := range ids {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()

			tx, err := database.BeginTx(ctx, nil)
			if err != nil {
				mu.Lock()
				failure = err
				mu.Unlock()
				return
			}
			defer tx.Rollback()

			v, err := svc.PostTx(ctx, tx, id, dan)
			if err == nil {
				err = tx.Commit()
			}

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failure = err
				return
			}
			if refNos[v.RefNo] {
				t.Errorf("Ref. No. %d was handed out twice", v.RefNo)
			}
			refNos[v.RefNo] = true
		}(id)
	}
	wg.Wait()

	if failure != nil {
		t.Fatalf("a concurrent post failed: %v", failure)
	}
	if len(refNos) != posters {
		t.Fatalf("%d posters produced %d distinct Ref. Nos.", posters, len(refNos))
	}
	for n := int64(1426); n < 1426+posters; n++ {
		if !refNos[n] {
			t.Errorf("Ref. No. %d is missing: the sequence has a gap in it", n)
		}
	}
}

// TestPostingOneDraftTwiceAtOnce is the double-clicked Post button, in its
// worst form: two requests in flight at the same moment.
func TestPostingOneDraftTwiceAtOnce(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	draft := mustCreate(t, svc, database, draftOf(t, 1, 500))

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			tx, err := database.BeginTx(ctx, nil)
			if err != nil {
				return
			}
			defer tx.Rollback()

			if _, err := svc.PostTx(ctx, tx, draft.ID, dan); err != nil {
				return
			}
			if err := tx.Commit(); err != nil {
				return
			}
			mu.Lock()
			succeeded++
			mu.Unlock()
		}()
	}
	wg.Wait()

	if succeeded != 1 {
		t.Errorf("%d of two simultaneous posts succeeded, want exactly one", succeeded)
	}
	if got := balanceOf(t, entries, 1).Principal.Centavos(); got != 50000 {
		t.Errorf("customer 1 owes %d centavos after a double-clicked Post, want 50000", got)
	}
}

func TestVoidingReturnsEveryBalanceExactlyToWhereItWas(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	// Something already on the books, so "back to where it was" is not the same
	// as "back to zero".
	earlier := mustCreate(t, svc, database, draftOf(t, 1, 200, 2, 400, 3, 600))
	mustPost(t, svc, database, earlier.ID, dan)

	before := map[int64]ledger.Balance{}
	for id := int64(1); id <= 3; id++ {
		before[id] = balanceOf(t, entries, id)
	}

	voucher := mustCreate(t, svc, database, draftOf(t, 1, 500, 2, 700, 3, 300))
	mustPost(t, svc, database, voucher.ID, dan)

	var voided Voucher
	err := inTx(t, database, func(tx *sql.Tx) error {
		var err error
		voided, err = svc.VoidTx(ctx, tx, voucher.ID, manager, "wrong farmer")
		return err
	})
	if err != nil {
		t.Fatalf("void: %v", err)
	}

	if voided.Status != StatusVoided || voided.VoidedAt == nil || voided.VoidReason != "wrong farmer" {
		t.Errorf("the voided voucher is %s, voided at %v, because %q", voided.Status, voided.VoidedAt, voided.VoidReason)
	}
	for id := int64(1); id <= 3; id++ {
		if after := balanceOf(t, entries, id); after.Principal != before[id].Principal {
			t.Errorf("customer %d is at %s after the void, was %s before the voucher",
				id, after.Principal, before[id].Principal)
		}
	}

	// Both halves stay on the record: money moved, and then it moved back.
	produced, err := svc.Entries(ctx, voucher.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(produced) != 6 {
		t.Fatalf("a voided three-line voucher shows %d entries, want 3 advances and 3 reversals", len(produced))
	}
	var reversals int
	for _, e := range produced {
		if e.IsReversal() {
			reversals++
			if e.Reason != "wrong farmer" {
				t.Errorf("a reversal gives the reason %q, want the one typed at the void", e.Reason)
			}
		}
	}
	if reversals != 3 {
		t.Errorf("the void wrote %d reversals for 3 lines", reversals)
	}

	// And a second void changes nothing.
	err = inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.VoidTx(ctx, tx, voucher.ID, manager, "again")
		return err
	})
	if !errors.Is(err, ErrNotPosted) {
		t.Errorf("voiding a voided voucher gave %v, want ErrNotPosted", err)
	}
}

func TestWhatVoidingRefuses(t *testing.T) {
	svc, _, database := openTestService(t)
	ctx := context.Background()

	draft := mustCreate(t, svc, database, draftOf(t, 1, 500))

	// A draft is deleted, not voided: it moved no money, so there is nothing to
	// reverse.
	err := inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.VoidTx(ctx, tx, draft.ID, manager, "changed my mind")
		return err
	})
	if !errors.Is(err, ErrNotPosted) {
		t.Errorf("voiding a draft gave %v, want ErrNotPosted", err)
	}

	mustPost(t, svc, database, draft.ID, dan)

	err = inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.VoidTx(ctx, tx, draft.ID, manager, "   ")
		return err
	})
	var ve ValidationError
	if !errors.As(err, &ve) || ve.Field != "reason" {
		t.Errorf("voiding with no reason gave %v, want a complaint about the reason", err)
	}
}
