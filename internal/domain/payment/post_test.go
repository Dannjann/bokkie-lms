package payment

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// The split is pure, so it is tested without a database at all. These are the
// cases somebody would have to reason about at a counter, written down.
func TestTheSplit(t *testing.T) {
	owed := func(principal, interest int64) ledger.Balance {
		return ledger.Balance{
			CustomerID: 1,
			Principal:  money.FromCentavos(principal),
			Interest:   money.FromCentavos(interest),
		}
	}

	cases := []struct {
		name                string
		amount              int64
		owed                ledger.Balance
		rule                Rule
		principal, interest int64
	}{
		{"interest first, both owed", 500000, owed(1200000, 60000), InterestFirst, 440000, 60000},
		{"interest first, no interest owed", 500000, owed(1200000, 0), InterestFirst, 500000, 0},
		{"interest first, interest exceeds the payment", 50000, owed(1200000, 60000), InterestFirst, 0, 50000},
		{"interest first, exactly the interest", 60000, owed(1200000, 60000), InterestFirst, 0, 60000},

		{"principal first, both owed", 500000, owed(1200000, 60000), PrincipalFirst, 500000, 0},
		{"principal first, principal exceeds the payment", 50000, owed(30000, 60000), PrincipalFirst, 30000, 20000},

		// Overpayment. Under either rule the excess lands on principal, so a
		// customer in credit is in credit on the principal they will next be
		// advanced.
		{"more than the whole debt, interest first", 200000, owed(100000, 50000), InterestFirst, 150000, 50000},
		{"more than the whole debt, principal first", 200000, owed(100000, 50000), PrincipalFirst, 150000, 50000},
		{"owing nothing at all", 100000, owed(0, 0), InterestFirst, 100000, 0},

		// A customer already in credit has a negative balance. An unclamped
		// min() would apply a negative amount of interest here — which is to
		// say, quietly charge them.
		{"already in credit, interest first", 100000, owed(-50000, -20000), InterestFirst, 100000, 0},
		{"already in credit, principal first", 100000, owed(-50000, -20000), PrincipalFirst, 100000, 0},
		{"in credit on interest only", 100000, owed(500000, -20000), InterestFirst, 100000, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			principal, interest, err := Split(money.FromCentavos(tc.amount), tc.owed, tc.rule)
			if err != nil {
				t.Fatalf("split: %v", err)
			}
			if principal.Centavos() != tc.principal || interest.Centavos() != tc.interest {
				t.Errorf("split %s → principal %s, interest %s; want %s and %s",
					money.FromCentavos(tc.amount), principal, interest,
					money.FromCentavos(tc.principal), money.FromCentavos(tc.interest))
			}

			// The invariant that holds over every row above, and which the
			// property tests then hammer over generated ones.
			total, err := principal.Add(interest)
			if err != nil {
				t.Fatalf("add the halves back up: %v", err)
			}
			if total.Centavos() != tc.amount {
				t.Errorf("the split of %s adds up to %s", money.FromCentavos(tc.amount), total)
			}
			if principal.IsNegative() || interest.IsNegative() {
				t.Errorf("a negative half: principal %s, interest %s", principal, interest)
			}
		})
	}
}

// Recording a payment numbers it and leaves the balance alone: the settlement
// is what applies it, after charging the interest it is meant to clear first.
func TestRecordingNumbersThePaymentAndLeavesTheBalance(t *testing.T) {
	svc, entries, database := openStationService(t, station{orNoStart: 4201})
	ctx := context.Background()

	owe(t, entries, 1, pesos(t, 12000), pesos(t, 600))
	p := mustPost(t, svc, database, mustCreate(t, svc, database, draftOf(t, 1, 5000)).ID, dan)

	if p.Status != StatusPosted {
		t.Errorf("a recorded payment is %s", p.Status)
	}
	if p.OrNo != 4201 {
		t.Errorf("the first payment took OR No. %d, want the sequence's start 4201", p.OrNo)
	}
	if p.PostedAt == nil || p.PostedBy.Username != "dan" {
		t.Errorf("recording did not say who did it: %v by %q", p.PostedAt, p.PostedBy.Username)
	}
	if p.SettlementID != 0 {
		t.Errorf("a payment nobody has settled says it was settled by %d", p.SettlementID)
	}

	after := balanceOf(t, entries, 1)
	if after.Principal.Centavos() != 1200000 || after.Interest.Centavos() != 60000 {
		t.Errorf("recording moved the balance to %s / %s", after.Principal, after.Interest)
	}
	written, err := svc.Entries(ctx, p.ID)
	if err != nil {
		t.Fatalf("read what recording wrote: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("recording wrote %d entries, want none", len(written))
	}

	waiting, err := svc.Unsettled(ctx, 1, ledger.MustDate(2099, time.January, 1))
	if err != nil {
		t.Fatalf("unsettled: %v", err)
	}
	if len(waiting) != 1 || waiting[0].ID != p.ID {
		t.Errorf("%d payments are waiting to be settled, want this one", len(waiting))
	}
}

func TestConcurrentPostsGetDistinctOrNos(t *testing.T) {
	const posters = 20

	svc, entries, database := openStationService(t, station{orNoStart: 7})
	ctx := context.Background()

	for id := int64(1); id <= 3; id++ {
		owe(t, entries, id, pesos(t, 100000), money.Zero)
	}

	ids := make([]int64, 0, posters)
	for i := 0; i < posters; i++ {
		ids = append(ids, mustCreate(t, svc, database, draftOf(t, int64(i%3)+1, 500)).ID)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		orNos   = make(map[int64]bool, posters)
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

			p, err := svc.PostTx(ctx, tx, id, dan)
			if err == nil {
				err = tx.Commit()
			}

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failure = err
				return
			}
			if orNos[p.OrNo] {
				t.Errorf("OR No. %d was handed out twice", p.OrNo)
			}
			orNos[p.OrNo] = true
		}(id)
	}
	wg.Wait()

	if failure != nil {
		t.Fatalf("a poster failed: %v", failure)
	}
	if len(orNos) != posters {
		t.Fatalf("%d posters produced %d OR Nos.", posters, len(orNos))
	}
	for n := int64(7); n < 7+posters; n++ {
		if !orNos[n] {
			t.Errorf("OR No. %d is missing from a sequence that should have no gaps", n)
		}
	}

}

// What a double-clicked Post button looks like from the server's side. A
// payment credited twice is worse than a voucher posted twice: it is wrong in
// the direction nobody complains about.
func TestTwoSimultaneousPostsOfOneDraftPostItOnce(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	owe(t, entries, 1, pesos(t, 5000), money.Zero)
	id := mustCreate(t, svc, database, draftOf(t, 1, 2000)).ID

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

			if _, err := svc.PostTx(ctx, tx, id, dan); err != nil {
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
	var numbered int
	if err := database.QueryRow(`SELECT COUNT(*) FROM payments WHERE or_no IS NOT NULL`).Scan(&numbered); err != nil {
		t.Fatal(err)
	}
	if numbered != 1 {
		t.Errorf("%d OR Nos. were handed out for one payment", numbered)
	}
	_ = entries
}

func TestPostingARolledBackTransactionMovesNothing(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	owe(t, entries, 1, pesos(t, 5000), money.Zero)
	id := mustCreate(t, svc, database, draftOf(t, 1, 2000)).ID

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PostTx(ctx, tx, id, dan); err != nil {
		t.Fatalf("post: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("roll back: %v", err)
	}

	if got := balanceOf(t, entries, 1).Principal.Centavos(); got != 500000 {
		t.Errorf("customer 1 owes %d centavos after a rolled-back post, want the original 500000", got)
	}
	p, err := svc.ByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != StatusDraft || p.OrNo != 0 {
		t.Errorf("after a rollback the payment is %s with OR No. %d, want an unnumbered draft", p.Status, p.OrNo)
	}
}

// orno.start is read at posting and only has an effect while the sequence has
// not begun. After that MAX wins — which is why the legacy number goes in
// before the first post at cutover.
func TestMovingTheSequenceStartAfterwardsIsIgnored(t *testing.T) {
	svc, entries, database := openStationService(t, station{orNoStart: 100})

	owe(t, entries, 1, pesos(t, 50000), money.Zero)
	first := mustPost(t, svc, database, mustCreate(t, svc, database, draftOf(t, 1, 500)).ID, dan)
	if first.OrNo != 100 {
		t.Fatalf("the first payment took OR No. %d, want 100", first.OrNo)
	}

	moved, _, _ := reopen(t, database, station{orNoStart: 5000})
	second := mustPost(t, moved, database, mustCreate(t, moved, database, draftOf(t, 1, 500)).ID, dan)
	if second.OrNo != 101 {
		t.Errorf("after moving orno.start to 5000 the next payment took %d, want 101", second.OrNo)
	}
}

// Voiding a payment nobody has settled writes nothing to the ledger, because
// recording it wrote nothing either.
func TestVoidingAnUnsettledPaymentMovesNothing(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	owe(t, entries, 1, pesos(t, 12000), pesos(t, 600))
	before := balanceOf(t, entries, 1)

	p := mustPost(t, svc, database, mustCreate(t, svc, database, draftOf(t, 1, 5000)).ID, dan)

	var voided Payment
	if err := inTx(t, database, func(tx *sql.Tx) error {
		var err error
		voided, err = svc.VoidTx(ctx, tx, p.ID, manager, "receipt written twice")
		return err
	}); err != nil {
		t.Fatalf("void: %v", err)
	}
	if voided.Status != StatusVoided || voided.VoidReason != "receipt written twice" {
		t.Errorf("voiding left it %s for %q", voided.Status, voided.VoidReason)
	}

	after := balanceOf(t, entries, 1)
	if after.Principal != before.Principal || after.Interest != before.Interest {
		t.Errorf("voiding moved the balance from %s/%s to %s/%s",
			before.Principal, before.Interest, after.Principal, after.Interest)
	}
	if written, err := svc.Entries(ctx, p.ID); err != nil || len(written) != 0 {
		t.Errorf("a voided, never-settled payment has %d entries (%v), want none", len(written), err)
	}
	if waiting, _ := svc.Unsettled(ctx, 1, today); len(waiting) != 0 {
		t.Errorf("a voided payment is still waiting to be settled")
	}
}

func TestWhatCannotBeVoided(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	owe(t, entries, 1, pesos(t, 5000), money.Zero)
	draft := mustCreate(t, svc, database, draftOf(t, 1, 1000))

	// A draft moved no money: there is nothing to reverse, and it is deleted.
	err := inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.VoidTx(ctx, tx, draft.ID, manager, "no")
		return err
	})
	if !errors.Is(err, ErrNotPosted) {
		t.Errorf("voiding a draft gave %v, want ErrNotPosted", err)
	}

	posted := mustPost(t, svc, database, draft.ID, dan)
	if err := inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.VoidTx(ctx, tx, posted.ID, manager, "wrong farmer")
		return err
	}); err != nil {
		t.Fatalf("void: %v", err)
	}

	// And a second void finds nothing in 'posted' to match.
	err = inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.VoidTx(ctx, tx, posted.ID, manager, "again")
		return err
	})
	if !errors.Is(err, ErrNotPosted) {
		t.Errorf("voiding twice gave %v, want ErrNotPosted", err)
	}

	// A void without a reason is refused before anything is written.
	err = inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.VoidTx(ctx, tx, posted.ID, manager, "   ")
		return err
	})
	var invalid ValidationError
	if !errors.As(err, &invalid) || invalid.Field != "reason" {
		t.Errorf("voiding with no reason gave %v, want a complaint about the reason", err)
	}
}

func TestAPostedPaymentCannotBeEditedOrDeleted(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	owe(t, entries, 1, pesos(t, 5000), money.Zero)
	p := mustPost(t, svc, database, mustCreate(t, svc, database, draftOf(t, 1, 1000)).ID, dan)

	err := inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.UpdateTx(ctx, tx, p.ID, draftOf(t, 1, 9999))
		return err
	})
	if !errors.Is(err, ErrNotDraft) {
		t.Errorf("editing a posted payment gave %v, want ErrNotDraft", err)
	}

	err = inTx(t, database, func(tx *sql.Tx) error {
		return svc.DeleteTx(ctx, tx, p.ID)
	})
	if !errors.Is(err, ErrNotDraft) {
		t.Errorf("deleting a posted payment gave %v, want ErrNotDraft", err)
	}
}

// The ledger and the document must agree about what a payment did before any of
// it is undone. A partial void is worse than a refused one, because it looks
// finished.
// A payment recorded before settlements existed wrote its own entry. Voiding
// it has to take that entry back, or the void refunds nothing.
func TestVoidingReversesAnEntryLeftFromBeforeSettlements(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	owe(t, entries, 1, pesos(t, 5000), money.Zero)
	p := mustPost(t, svc, database, mustCreate(t, svc, database, draftOf(t, 1, 1000)).ID, dan)
	if _, err := entries.Post(ctx, ledger.Entry{
		CustomerID: 1, Date: today, Type: ledger.TypePayment,
		Principal: pesos(t, -1000), Interest: money.Zero,
		SourceTable: SourceTable, SourceID: p.ID, By: dan,
	}); err != nil {
		t.Fatalf("plant the old entry: %v", err)
	}

	if err := inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.VoidTx(ctx, tx, p.ID, manager, "tidy up")
		return err
	}); err != nil {
		t.Fatalf("void: %v", err)
	}
	if got := balanceOf(t, entries, 1).Principal.Centavos(); got != 500000 {
		t.Errorf("after the void the farmer owes %d centavos, want 500000", got)
	}
}
