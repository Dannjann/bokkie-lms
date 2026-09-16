package payment

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	bokkiedb "github.com/Dannjann/bokkie-lms/internal/db"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// The example-based tests check the cases somebody thought of. This one
// generates sequences nobody thought of — advances, interest charges, payment
// drafts edited and abandoned, posted and voided, interleaved across customers
// and under both application orders — and asserts the six things that have to
// hold for a payment to be worth anything:
//
//  1. A posted payment moves what is owed down by exactly its amount.
//  2. The split adds up to the amount, and neither half is negative.
//  3. A payment never clears more of a component than was owed on it, and the
//     rule decides which one goes first.
//  4. A voided payment puts both halves of the balance exactly back.
//  5. A statement's closing balance equals the balance the rest of the system
//     shows, at any cut date.
//  6. OR Nos. are unique, consecutive from orno.start, and never reused.
//
// A counterexample here is a balance a farmer could have disputed.
//
// The model deliberately tracks only the *combined* debt, which needs no
// knowledge of how a payment divides. That keeps it from being a second copy of
// Split that would agree with a wrong one — the division itself is checked
// against the properties above rather than against a reimplementation.

const (
	propertyCustomers = 3
	propertyOrNoStart = 4201
)

var propertySeq atomic.Int64

func newPropertyService(t *rapid.T, dir string, rule Rule) (*Service, *ledger.Service, *sql.DB) {
	t.Helper()
	ctx := context.Background()

	path := filepath.Join(dir, fmt.Sprintf("payments-%d.db", propertySeq.Add(1)))
	database, err := bokkiedb.Open(ctx, path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := bokkiedb.Migrate(ctx, database, log); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for id, name := range map[int64]string{1: "dan", 2: "manager"} {
		if _, err := database.Exec(
			`INSERT INTO users (id, username, display_name, password_hash, role, created_at)
			 VALUES (?, ?, ?, 'x', 'manager', '2026-01-01T00:00:00Z')`, id, name, name); err != nil {
			t.Fatalf("add user: %v", err)
		}
	}
	for id := int64(1); id <= propertyCustomers; id++ {
		if _, err := database.Exec(
			`INSERT INTO customers (id, code, name, created_at, updated_at)
			 VALUES (?, ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
			id, fmt.Sprintf("BA-%04d", id), fmt.Sprintf("Customer %d", id)); err != nil {
			t.Fatalf("add customer: %v", err)
		}
	}

	st := station{orNoStart: propertyOrNoStart, applies: rule}
	entries := ledger.New(database, st, audit.New(database, log), log)
	svc := New(database, entries, st, log)
	svc.now = func() time.Time { return time.Date(2026, 3, 2, 9, 30, 0, 0, st.Location()) }
	return svc, entries, database
}

// genDraft draws a payment that is always valid, so a failure is never just the
// generator producing nonsense. Amounts run well past what a customer is likely
// to owe, because overpayment is allowed and is where the interesting cases are.
func genDraft(t *rapid.T, label string) Draft {
	return Draft{
		CustomerID: rapid.Int64Range(1, propertyCustomers).Draw(t, label+"/customer"),
		Date:       today.AddDays(-rapid.IntRange(0, 60).Draw(t, label+"/day")),
		Amount:     money.FromCentavos(rapid.Int64Range(1, 5_000_000).Draw(t, label+"/amount")),
		Method:     rapid.SampledFrom(Methods).Draw(t, label+"/method"),
		Particular: "generated",
	}
}

func TestPropertyPaymentsHoldUnderArbitrarySequences(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		rule := rapid.SampledFrom([]Rule{InterestFirst, PrincipalFirst}).Draw(rt, "rule")
		svc, entries, database := newPropertyService(rt, dir, rule)

		// The model: what each customer owes in total, principal and interest
		// together. A payment reduces it by its amount and a void puts that
		// back, neither of which needs to know how the payment divided.
		owed := map[int64]int64{}

		// What each posted payment applied, so a void can be checked against
		// the halves it actually moved rather than against the halves a second
		// implementation thinks it should have moved.
		applied := map[int64][2]int64{} // payment id → {principal, interest}

		var drafts, live []int64 // ids of drafts, and of posted-not-voided payments
		nextOrNo := int64(propertyOrNoStart)
		seenOrNos := map[int64]bool{}

		run := func(fn func(tx *sql.Tx) error) error {
			tx, err := database.BeginTx(ctx, nil)
			if err != nil {
				rt.Fatalf("begin: %v", err)
			}
			defer tx.Rollback()

			if err := fn(tx); err != nil {
				return err
			}
			if err := tx.Commit(); err != nil {
				rt.Fatalf("commit: %v", err)
			}
			return nil
		}

		drop := func(ids []int64, id int64) []int64 {
			out := ids[:0]
			for _, candidate := range ids {
				if candidate != id {
					out = append(out, candidate)
				}
			}
			return out
		}

		balanceOf := func(customerID int64) ledger.Balance {
			b, err := entries.BalanceFor(ctx, customerID)
			if err != nil {
				rt.Fatalf("balance for customer %d: %v", customerID, err)
			}
			return b
		}

		steps := rapid.IntRange(1, 20).Draw(rt, "steps")
		for step := 0; step < steps; step++ {
			label := fmt.Sprintf("step%d", step)

			switch rapid.SampledFrom([]string{"lend", "charge", "create", "edit", "delete", "post", "void"}).
				Draw(rt, label+"/action") {

			case "lend":
				customerID := rapid.Int64Range(1, propertyCustomers).Draw(rt, label+"/customer")
				amount := money.FromCentavos(rapid.Int64Range(1, 5_000_000).Draw(rt, label+"/amount"))
				if _, err := entries.Post(ctx, ledger.Entry{
					CustomerID: customerID, Date: today.AddDays(-30), Type: ledger.TypeAdvance,
					Principal: amount, By: dan,
				}); err != nil {
					rt.Fatalf("lend: %v", err)
				}
				owed[customerID] += amount.Centavos()

			case "charge":
				// Interest does not exist until step 7, but the ledger can hold
				// it today and a payment has to divide correctly when it does.
				// Generating it here is what stops step 7 from being the first
				// time the interest half of the split is ever exercised.
				customerID := rapid.Int64Range(1, propertyCustomers).Draw(rt, label+"/customer")
				amount := money.FromCentavos(rapid.Int64Range(1, 200_000).Draw(rt, label+"/amount"))
				if _, err := entries.Post(ctx, ledger.Entry{
					CustomerID: customerID, Date: today.AddDays(-15), Type: ledger.TypeInterest,
					Interest: amount, By: dan,
				}); err != nil {
					rt.Fatalf("charge interest: %v", err)
				}
				owed[customerID] += amount.Centavos()

			case "create":
				var p Payment
				if err := run(func(tx *sql.Tx) error {
					var err error
					p, err = svc.CreateTx(ctx, tx, genDraft(rt, label), dan)
					return err
				}); err != nil {
					rt.Fatalf("create a draft: %v", err)
				}
				if p.OrNo != 0 {
					rt.Fatalf("a draft was numbered %d", p.OrNo)
				}
				drafts = append(drafts, p.ID)

			case "edit":
				if len(drafts) == 0 {
					continue
				}
				id := rapid.SampledFrom(drafts).Draw(rt, label+"/draft")
				if err := run(func(tx *sql.Tx) error {
					_, err := svc.UpdateTx(ctx, tx, id, genDraft(rt, label))
					return err
				}); err != nil {
					rt.Fatalf("edit draft %d: %v", id, err)
				}

			case "delete":
				if len(drafts) == 0 {
					continue
				}
				id := rapid.SampledFrom(drafts).Draw(rt, label+"/draft")
				if err := run(func(tx *sql.Tx) error { return svc.DeleteTx(ctx, tx, id) }); err != nil {
					rt.Fatalf("delete draft %d: %v", id, err)
				}
				drafts = drop(drafts, id)

			case "post":
				if len(drafts) == 0 {
					continue
				}
				id := rapid.SampledFrom(drafts).Draw(rt, label+"/draft")

				draft, err := svc.ByID(ctx, id)
				if err != nil {
					rt.Fatalf("read draft %d: %v", id, err)
				}
				before := balanceOf(draft.CustomerID)

				var p Payment
				if err := run(func(tx *sql.Tx) error {
					var err error
					p, err = svc.PostTx(ctx, tx, id, dan)
					return err
				}); err != nil {
					rt.Fatalf("post draft %d: %v", id, err)
				}

				// Invariant 6: consecutive from the setting, and never twice.
				if p.OrNo != nextOrNo {
					rt.Fatalf("payment %d was numbered %d, want %d", id, p.OrNo, nextOrNo)
				}
				if seenOrNos[p.OrNo] {
					rt.Fatalf("OR No. %d was handed out twice", p.OrNo)
				}
				seenOrNos[p.OrNo] = true
				nextOrNo++

				principal, interest := assertTheSplit(rt, svc, p, before, rule)
				applied[p.ID] = [2]int64{principal, interest}

				owed[p.CustomerID] -= p.Amount.Centavos()
				drafts = drop(drafts, id)
				live = append(live, id)

			case "void":
				if len(live) == 0 {
					continue
				}
				id := rapid.SampledFrom(live).Draw(rt, label+"/payment")

				var p Payment
				if err := run(func(tx *sql.Tx) error {
					var err error
					p, err = svc.VoidTx(ctx, tx, id, manager, "generated void")
					return err
				}); err != nil {
					rt.Fatalf("void payment %d: %v", id, err)
				}

				// Invariant 4: both halves back, exactly as they were moved.
				after := balanceOf(p.CustomerID)
				was := applied[p.ID]
				if err := assertVoidRestored(rt, svc, p, after, was); err != nil {
					rt.Fatal(err)
				}

				owed[p.CustomerID] += p.Amount.Centavos()
				live = drop(live, id)
			}

			// Invariant 1, and the check that drafts move nothing: the model
			// knows only the combined debt, so anything a draft did to a real
			// balance shows up here.
			for id := int64(1); id <= propertyCustomers; id++ {
				b := balanceOf(id)
				total, err := b.Total()
				if err != nil {
					rt.Fatalf("total the balance of customer %d: %v", id, err)
				}
				if total.Centavos() != owed[id] {
					rt.Fatalf("after %s, customer %d owes %d centavos in total, want %d",
						label, id, total.Centavos(), owed[id])
				}
			}
		}

		// Invariant 5, at the end and at a cut somewhere in the middle: a
		// statement's closing balance is the balance the rest of the system
		// shows.
		cut := today.AddDays(-rapid.IntRange(0, 60).Draw(rt, "cut"))
		for id := int64(1); id <= propertyCustomers; id++ {
			stmt, err := entries.Statement(ctx, id, ledger.Date{}, ledger.Date{})
			if err != nil {
				rt.Fatalf("statement for customer %d: %v", id, err)
			}
			cached := balanceOf(id)
			if stmt.Closing.Principal != cached.Principal || stmt.Closing.Interest != cached.Interest {
				rt.Fatalf("customer %d: the statement closes at %s/%s and the balance says %s/%s",
					id, stmt.Closing.Principal, stmt.Closing.Interest, cached.Principal, cached.Interest)
			}
			if n := len(stmt.Rows); n > 0 {
				last := stmt.Rows[n-1].Running
				if last.Principal != stmt.Closing.Principal || last.Interest != stmt.Closing.Interest {
					rt.Fatalf("customer %d: the last row runs to %s/%s but the statement closes at %s/%s",
						id, last.Principal, last.Interest, stmt.Closing.Principal, stmt.Closing.Interest)
				}
			}

			partial, err := entries.Statement(ctx, id, ledger.Date{}, cut)
			if err != nil {
				rt.Fatalf("statement for customer %d up to %s: %v", id, cut, err)
			}
			at, err := entries.BalanceAt(ctx, id, cut)
			if err != nil {
				rt.Fatalf("balance at %s for customer %d: %v", cut, id, err)
			}
			if partial.Closing.Principal != at.Principal || partial.Closing.Interest != at.Interest {
				rt.Fatalf("customer %d: a statement to %s closes at %s/%s, BalanceAt says %s/%s",
					id, cut, partial.Closing.Principal, partial.Closing.Interest, at.Principal, at.Interest)
			}
		}

		// And the ledger's own first invariant, over a ledger written by real
		// documents rather than by a generator.
		found, err := entries.Discrepancies(ctx)
		if err != nil {
			rt.Fatalf("compare the cache against the ledger: %v", err)
		}
		if len(found) != 0 {
			rt.Fatalf("the cached balances disagree with the entries behind them: %v", found)
		}
	})
}

// assertTheSplit is invariants 2 and 3, checked against the entry that was
// actually written rather than against a second implementation of the rule.
//
// It returns what was applied to each half, so that voiding can be checked
// against the same figures.
func assertTheSplit(t *rapid.T, svc *Service, p Payment, before ledger.Balance, rule Rule) (principal, interest int64) {
	t.Helper()

	written, err := svc.Entries(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("read what payment %d wrote: %v", p.ID, err)
	}
	if len(written) != 1 {
		t.Fatalf("payment %d wrote %d entries, want one", p.ID, len(written))
	}
	e := written[0]

	// The deltas are negative; what was *applied* is their negation.
	principal, interest = -e.Principal.Centavos(), -e.Interest.Centavos()

	// Invariant 2: the split divides the payment, it never changes its size.
	if principal+interest != p.Amount.Centavos() {
		t.Fatalf("payment %d of %s split into %d and %d", p.ID, p.Amount, principal, interest)
	}
	if principal < 0 || interest < 0 {
		t.Fatalf("payment %d split into a negative half: %d and %d", p.ID, principal, interest)
	}

	// Invariant 3, stated as properties rather than as a second implementation
	// of the rule — which is the whole point of checking it here.
	owedInterest, owedPrincipal := before.Interest.Centavos(), before.Principal.Centavos()
	amount := p.Amount.Centavos()

	atMost := func(owed int64) int64 {
		if owed < 0 {
			return 0
		}
		return owed
	}
	debt := owedPrincipal + owedInterest

	// Never clears more interest than was owed. This holds under both rules:
	// under interest-first because interest is taken first and capped, and
	// under principal-first because the excess beyond the whole debt goes to
	// principal rather than to interest.
	if interest > atMost(owedInterest) {
		t.Fatalf("payment %d cleared %d of interest, more than the %d owed", p.ID, interest, owedInterest)
	}

	// Never clears more principal than was owed, unless the payment was for
	// more than the whole debt — in which case the excess lands here and the
	// customer is in credit.
	if principal > atMost(owedPrincipal) && amount <= debt {
		t.Fatalf("payment %d of %d cleared %d of principal against %d owed, without exceeding the debt of %d",
			p.ID, amount, principal, owedPrincipal, debt)
	}

	// And the rule decides which component gets first call on the money.
	switch rule {
	case InterestFirst:
		if owedInterest > 0 && interest != min64(amount, owedInterest) {
			t.Fatalf("payment %d of %d put %d against the %d of interest owed, under interest-first",
				p.ID, amount, interest, owedInterest)
		}
		if owedInterest <= 0 && interest != 0 {
			t.Fatalf("payment %d put %d against interest when none was owed (%d)", p.ID, interest, owedInterest)
		}
	case PrincipalFirst:
		if owedPrincipal > 0 && principal < min64(amount, owedPrincipal) {
			t.Fatalf("payment %d of %d put only %d against the %d of principal owed, under principal-first",
				p.ID, amount, principal, owedPrincipal)
		}
	}

	return principal, interest
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// assertVoidRestored is invariant 4: a voided payment puts back exactly the two
// halves it took, and leaves both itself and its reversal on the record.
func assertVoidRestored(t *rapid.T, svc *Service, p Payment, after ledger.Balance, was [2]int64) error {
	t.Helper()

	written, err := svc.Entries(context.Background(), p.ID)
	if err != nil {
		return fmt.Errorf("read what payment %d wrote: %w", p.ID, err)
	}
	if len(written) != 2 {
		return fmt.Errorf("a voided payment has %d entries, want the payment and its reversal", len(written))
	}
	if !written[1].IsReversal() {
		return fmt.Errorf("the second entry of voided payment %d is a %s", p.ID, written[1].Type)
	}

	// The reversal is the exact negation of what was applied.
	if written[1].Principal.Centavos() != was[0] || written[1].Interest.Centavos() != was[1] {
		return fmt.Errorf("payment %d applied %d/%d and its reversal moved %s/%s",
			p.ID, was[0], was[1], written[1].Principal, written[1].Interest)
	}
	_ = after
	return nil
}
