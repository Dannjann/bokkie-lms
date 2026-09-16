package interest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
)

func TestPostingChargesEveryBorrowerAtOnce(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	lend(t, entries, 1, pesos(t, 5000))
	lend(t, entries, 2, pesos(t, 1000))

	run := mustPost(t, svc, database, mustCreate(t, svc, database, september()).ID, manager)

	if run.RunNo != 1 {
		t.Errorf("the first run is numbered %d, want 1", run.RunNo)
	}

	for _, want := range []struct {
		customer int64
		interest string
	}{{1, "150.00"}, {2, "30.00"}} {
		got := balanceOf(t, entries, want.customer)
		if got.Interest.String() != want.interest {
			t.Errorf("customer %d owes %s of interest, want %s", want.customer, got.Interest, want.interest)
		}
		// Principal is untouched: an interest entry charges interest, and
		// ledger.Entry.Validate refuses anything else.
		if want.customer == 1 && got.Principal.String() != "5000.00" {
			t.Errorf("customer 1's principal moved to %s", got.Principal)
		}
	}

	posted, err := svc.Entries(ctx, run.ID)
	if err != nil {
		t.Fatalf("read what the run wrote: %v", err)
	}
	if len(posted) != 2 {
		t.Fatalf("the run wrote %d entries, want one per line", len(posted))
	}
	for _, e := range posted {
		if e.Type != ledger.TypeInterest {
			t.Errorf("the run wrote a %s entry", e.Type)
		}
		// The end of the period, not the day somebody clicked. A run for
		// September charges September even when it is posted in March.
		if e.Date != septemberTo {
			t.Errorf("an entry is dated %s, want the end of the period, %s", e.Date, septemberTo)
		}
		if e.SourceTable != SourceTable {
			t.Errorf("an entry points at %q, want %q", e.SourceTable, SourceTable)
		}
	}
}

func TestARunIsPostedOnce(t *testing.T) {
	svc, entries, database := openTestService(t)
	lend(t, entries, 1, pesos(t, 5000))

	run := mustCreate(t, svc, database, september())
	mustPost(t, svc, database, run.ID, manager)

	if _, err := post(t, svc, database, run.ID, manager); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("posting a second time: got %v, want ErrNotDraft", err)
	}
	if got := balanceOf(t, entries, 1).Interest.String(); got != "150.00" {
		t.Errorf("customer 1 owes %s of interest after a double post, want 150.00", got)
	}
}

func TestARunWithNothingToChargeIsNotPosted(t *testing.T) {
	svc, _, database := openTestService(t)

	// Nobody has borrowed anything, so the run has no lines. A posted run that
	// wrote no entries would be a document saying something happened when
	// nothing did.
	run := mustCreate(t, svc, database, september())
	if len(run.Lines) != 0 {
		t.Fatalf("a run over an empty ledger has %d lines", len(run.Lines))
	}

	if _, err := post(t, svc, database, run.ID, manager); !errors.Is(err, ErrNothingToCharge) {
		t.Fatalf("posting an empty run: got %v, want ErrNothingToCharge", err)
	}
	if status := statusOf(t, database, run.ID); status != string(StatusDraft) {
		t.Errorf("the run is %s after a refused post, want draft", status)
	}
}

// The overlap guard: the half an index cannot express.
func TestAPeriodThatOverlapsAPostedRunIsRefused(t *testing.T) {
	svc, entries, database := openTestService(t)
	lend(t, entries, 1, pesos(t, 5000))

	first := mustPost(t, svc, database, mustCreate(t, svc, database, september()).ID, manager)

	overlapping := mustCreate(t, svc, database, Draft{
		From: septemberTo.AddDays(-15), // 15 September
		To:   septemberTo.AddDays(15),  // 15 October
	})

	_, err := post(t, svc, database, overlapping.ID, manager)
	if !errors.Is(err, ErrPeriodAlreadyCharged) {
		t.Fatalf("posting an overlapping period: got %v, want ErrPeriodAlreadyCharged", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("run %d", first.RunNo)) {
		t.Errorf("the refusal is %q; it should name the run that already covers it", err)
	}

	// And it changed nothing: still a draft, still one charge on the books.
	if status := statusOf(t, database, overlapping.ID); status != string(StatusDraft) {
		t.Errorf("the refused run is %s, want draft", status)
	}
	if got := balanceOf(t, entries, 1).Interest.String(); got != "150.00" {
		t.Errorf("customer 1 owes %s of interest, want the one charge at 150.00", got)
	}
}

func TestAPeriodThatTouchesNoPostedRunIsFine(t *testing.T) {
	svc, entries, database := openTestService(t)
	lend(t, entries, 1, pesos(t, 5000))

	mustPost(t, svc, database, mustCreate(t, svc, database, september()).ID, manager)

	october := mustCreate(t, svc, database, Draft{
		From: septemberTo.AddDays(1),
		To:   septemberTo.AddDays(31),
	})
	if _, err := post(t, svc, database, october.ID, manager); err != nil {
		t.Fatalf("posting the month after: %v", err)
	}
	// 150.00 in September, then 3% of the same 5,000 of principal in October.
	// September's charge is not itself charged on: the basis is principal, and
	// that is the whole difference between simple and compound interest.
	if got := balanceOf(t, entries, 1).Interest.String(); got != "300.00" {
		t.Errorf("customer 1 owes %s of interest after two months, want 300.00", got)
	}
}

// The heart of the step: a manager approved specific figures, and if the ledger
// has moved since then those are no longer the figures that would be written.
func TestAPaymentBetweenThePreviewAndThePostRefusesTheRun(t *testing.T) {
	svc, entries, database := openTestService(t)
	lend(t, entries, 1, pesos(t, 5000))

	run := mustCreate(t, svc, database, september())
	if run.Lines[0].Charge.String() != "150.00" {
		t.Fatalf("the preview charges %s, want 150.00", run.Lines[0].Charge)
	}

	// A farmer pays at the counter while the run sits on somebody's screen.
	// Entirely ordinary, and it changes what September's charge would be.
	repay(t, entries, 1, septemberTo, pesos(t, 2000))

	_, err := post(t, svc, database, run.ID, manager)
	if !errors.Is(err, ErrPreviewStale) {
		t.Fatalf("posting a stale preview: got %v, want ErrPreviewStale", err)
	}
	if !strings.Contains(err.Error(), "BA-0001") {
		t.Errorf("the refusal is %q; it should name the farmer whose figure moved", err)
	}

	if got := balanceOf(t, entries, 1).Interest; !got.IsZero() {
		t.Errorf("a refused post charged %s anyway", got)
	}
	if status := statusOf(t, database, run.ID); status != string(StatusDraft) {
		t.Errorf("the refused run is %s, want draft", status)
	}

	// Recomputing is the fix, and it is one click.
	var fresh Run
	if err := inTx(t, database, func(tx *sql.Tx) error {
		var err error
		fresh, err = svc.UpdateTx(context.Background(), tx, run.ID, september())
		return err
	}); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if fresh.Lines[0].Charge.String() != "90.00" {
		t.Fatalf("the recomputed charge is %s, want 90.00", fresh.Lines[0].Charge)
	}
	if _, err := post(t, svc, database, run.ID, manager); err != nil {
		t.Fatalf("posting the recomputed run: %v", err)
	}
}

// A farmer who starts owing something between the preview and the post is the
// same fault from the other side, and it is caught at the position they would
// have taken in the run.
func TestANewBorrowerBetweenThePreviewAndThePostRefusesTheRun(t *testing.T) {
	svc, entries, database := openTestService(t)
	lend(t, entries, 1, pesos(t, 5000))

	run := mustCreate(t, svc, database, september())

	lend(t, entries, 2, pesos(t, 1000))

	if _, err := post(t, svc, database, run.ID, manager); !errors.Is(err, ErrPreviewStale) {
		t.Fatalf("posting a run that is missing a borrower: got %v, want ErrPreviewStale", err)
	}
}

func TestVoidingPutsEveryBalanceBackExactly(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	lend(t, entries, 1, pesos(t, 5000))
	lend(t, entries, 2, pesos(t, 1000))

	before := []ledger.Balance{balanceOf(t, entries, 1), balanceOf(t, entries, 2)}
	run := mustPost(t, svc, database, mustCreate(t, svc, database, september()).ID, manager)

	voided := mustVoid(t, svc, database, run.ID, "the rate was wrong")
	if voided.Status != StatusVoided {
		t.Errorf("the run is %s after voiding", voided.Status)
	}

	for i, customer := range []int64{1, 2} {
		got := balanceOf(t, entries, customer)
		if got.Principal != before[i].Principal || got.Interest != before[i].Interest {
			t.Errorf("customer %d owes %s / %s after the void, want %s / %s",
				customer, got.Principal, got.Interest, before[i].Principal, before[i].Interest)
		}
	}

	// Both the charge and its reversal stay on the record: what happened is
	// that interest was charged and then taken back, not that it was never
	// charged.
	posted, err := svc.Entries(ctx, run.ID)
	if err != nil {
		t.Fatalf("read the run's entries: %v", err)
	}
	if len(posted) != 4 {
		t.Fatalf("the voided run shows %d entries, want two charges and two reversals", len(posted))
	}
	for _, e := range posted {
		if e.IsReversal() && e.Reason != "the rate was wrong" {
			t.Errorf("a reversal carries the reason %q", e.Reason)
		}
	}
}

func TestVoidingNeedsAReasonAndHappensOnce(t *testing.T) {
	svc, entries, database := openTestService(t)
	lend(t, entries, 1, pesos(t, 5000))

	run := mustPost(t, svc, database, mustCreate(t, svc, database, september()).ID, manager)

	err := inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.VoidTx(context.Background(), tx, run.ID, manager, "   ")
		return err
	})
	var invalid ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("voiding with no reason: got %v, want a validation error", err)
	}

	mustVoid(t, svc, database, run.ID, "wrong rate")

	err = inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.VoidTx(context.Background(), tx, run.ID, manager, "again")
		return err
	})
	if !errors.Is(err, ErrNotPosted) {
		t.Fatalf("voiding twice: got %v, want ErrNotPosted", err)
	}
}

// A run voided because the rate was wrong has to be re-runnable, which is why
// the period index is partial on 'posted'.
func TestVoidingFreesThePeriodToBeChargedAgain(t *testing.T) {
	svc, entries, database := openTestService(t)
	lend(t, entries, 1, pesos(t, 5000))

	first := mustPost(t, svc, database, mustCreate(t, svc, database, september()).ID, manager)
	mustVoid(t, svc, database, first.ID, "wrong rate")

	dearer, _, _ := reopen(t, database, station{method: MethodMonthlyOnBalance, rateBps: 500, basis: BasisPrincipal})
	again := mustPost(t, dearer, database, mustCreate(t, dearer, database, september()).ID, manager)

	if again.RunNo != 2 {
		t.Errorf("the second run is numbered %d, want 2 — the sequence does not reuse numbers", again.RunNo)
	}
	if got := balanceOf(t, entries, 1).Interest.String(); got != "250.00" {
		t.Errorf("customer 1 owes %s of interest, want the second run's 250.00", got)
	}
}

func TestARolledBackPostLeavesNothingBehind(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()
	lend(t, entries, 1, pesos(t, 5000))

	run := mustCreate(t, svc, database, september())

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := svc.PostTx(ctx, tx, run.ID, manager); err != nil {
		t.Fatalf("post inside a transaction: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("roll back: %v", err)
	}

	if got := balanceOf(t, entries, 1).Interest; !got.IsZero() {
		t.Errorf("a rolled-back post left %s of interest on the books", got)
	}
	if status := statusOf(t, database, run.ID); status != string(StatusDraft) {
		t.Errorf("the run is %s after a rollback, want draft", status)
	}
}

// Twenty managers posting twenty runs at once. This is the test that fails —
// as SQLITE_BUSY rather than as a duplicate number — if the run number is ever
// allocated by reading MAX and then writing it back.
func TestConcurrentPostsTakeDistinctNumbers(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	lend(t, entries, 1, pesos(t, 5000))

	// Twenty single-day periods that cannot overlap each other, so what is
	// being tested is the allocator and not the overlap guard.
	const posters = 20
	ids := make([]int64, posters)
	for i := range ids {
		day := ledger.MustDate(2026, time.January, 1).AddDays(i)
		ids[i] = mustCreate(t, svc, database, Draft{From: day, To: day}).ID
	}

	var wg sync.WaitGroup
	errs := make([]error, posters)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = inTx(t, database, func(tx *sql.Tx) error {
				_, err := svc.PostTx(ctx, tx, ids[i], manager)
				return err
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("poster %d: %v", i, err)
		}
	}

	rows, err := database.Query(`SELECT run_no FROM interest_runs WHERE run_no IS NOT NULL ORDER BY run_no`)
	if err != nil {
		t.Fatalf("read the numbers: %v", err)
	}
	defer rows.Close()

	var numbers []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		numbers = append(numbers, n)
	}
	if len(numbers) != posters {
		t.Fatalf("%d runs posted, want %d", len(numbers), posters)
	}
	for i, n := range numbers {
		if n != int64(i+1) {
			t.Fatalf("the numbers are %v; want 1..%d with no gaps and no repeats", numbers, posters)
		}
	}
}

// Twenty clicks on Post, on one run. It charges once.
func TestTwentySimultaneousPostsOfOneRunChargeOnce(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	lend(t, entries, 1, pesos(t, 5000))
	run := mustCreate(t, svc, database, september())

	var wg sync.WaitGroup
	var mu sync.Mutex
	var posted int
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := inTx(t, database, func(tx *sql.Tx) error {
				_, err := svc.PostTx(ctx, tx, run.ID, manager)
				return err
			})
			if err == nil {
				mu.Lock()
				posted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if posted != 1 {
		t.Errorf("%d of twenty posts succeeded, want exactly one", posted)
	}
	if got := balanceOf(t, entries, 1).Interest.String(); got != "150.00" {
		t.Errorf("customer 1 owes %s of interest, want a single charge of 150.00", got)
	}
}

func statusOf(t *testing.T, database *sql.DB, id int64) string {
	t.Helper()

	var status string
	if err := database.QueryRow(`SELECT status FROM interest_runs WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatalf("read the status of run %d: %v", id, err)
	}
	return status
}

// The identical period, as opposed to the merely overlapping one. It is worth
// its own test because it is the case the unique index would otherwise catch
// first — as a constraint failure nobody can read, rather than as a sentence
// naming the run that already covers it. The guard lives in the posting
// UPDATE's WHERE clause so that this and the overlapping case above answer the
// same way.
func TestTheSamePeriodTwiceIsRefusedWithTheRunNamed(t *testing.T) {
	svc, entries, database := openTestService(t)
	lend(t, entries, 1, pesos(t, 5000))

	first := mustPost(t, svc, database, mustCreate(t, svc, database, september()).ID, manager)
	again := mustCreate(t, svc, database, september())

	_, err := post(t, svc, database, again.ID, manager)
	if !errors.Is(err, ErrPeriodAlreadyCharged) {
		t.Fatalf("charging the same period twice: got %v, want ErrPeriodAlreadyCharged", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("run %d", first.RunNo)) {
		t.Errorf("the refusal is %q; it should name the run that already covers it", err)
	}
	if status := statusOf(t, database, again.ID); status != string(StatusDraft) {
		t.Errorf("the refused run is %s, want draft", status)
	}
	if got := balanceOf(t, entries, 1).Interest.String(); got != "150.00" {
		t.Errorf("customer 1 owes %s of interest, want the one charge at 150.00", got)
	}
}
