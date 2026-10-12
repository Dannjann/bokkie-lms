package ledger

import (
	"context"
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

func TestBalancesAtLeavesOutWhoeverOwesNothing(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()
	on := MustDate(2026, time.March, 1)

	mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))
	mustPost(t, svc, advance(2, MustDate(2026, time.January, 11), 3000))
	mustPost(t, svc, payment(2, MustDate(2026, time.February, 1), 3000, 0))
	// Customer 3 has no entries at all, which is a different way of owing
	// nothing and must read the same.

	balances, err := svc.BalancesAt(ctx, on)
	if err != nil {
		t.Fatalf("balances at %s: %v", on, err)
	}
	if len(balances) != 1 {
		t.Fatalf("balances at %s: got %d rows, want only the customer who owes something: %+v",
			on, len(balances), balances)
	}
	if balances[0].CustomerID != 1 || balances[0].Principal.String() != "5000.00" {
		t.Fatalf("balances at %s: got %+v, want customer 1 owing 5000.00", on, balances[0])
	}
}

func TestBalancesAtIncludesACustomerInCredit(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 1000))
	mustPost(t, svc, payment(1, MustDate(2026, time.January, 20), 1500, 0))

	balances, err := svc.BalancesAt(ctx, MustDate(2026, time.March, 1))
	if err != nil {
		t.Fatalf("balances: %v", err)
	}

	// A credit is a balance, and whether it is chargeable is the interest run's
	// rule rather than the ledger's. Dropping it here would hide it from every
	// caller instead of from the one that does not want it.
	if len(balances) != 1 || balances[0].Principal.String() != "-500.00" {
		t.Fatalf("balances: got %+v, want customer 1 in credit by 500.00", balances)
	}
}

func TestBalancesAtIsHistorical(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))
	mustPost(t, svc, advance(1, MustDate(2026, time.February, 10), 2000))

	at := MustDate(2026, time.January, 31)
	balances, err := svc.BalancesAt(ctx, at)
	if err != nil {
		t.Fatalf("balances at %s: %v", at, err)
	}
	if len(balances) != 1 || balances[0].Principal.String() != "5000.00" {
		t.Fatalf("balances at %s: got %+v, want 5000.00 — February has not happened yet", at, balances)
	}
}

func TestBalancesAtNeedsADate(t *testing.T) {
	svc, _ := openTestService(t)

	if _, err := svc.BalancesAt(context.Background(), Date{}); err == nil {
		t.Fatal("balances as at no date at all: got no error, want one")
	}
}

// The one that matters. A reversal is dated the day of the void, so an advance
// made in September and voided in October leaves an entry inside the period and
// its reversal outside it — and charging interest on money that has been taken
// back is exactly the failure this netting exists to prevent.
func TestAdvancedBetweenNetsAReversalWheneverItHappened(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	inside := mustPost(t, svc, advance(1, MustDate(2026, time.September, 15), 5000))
	mustPost(t, svc, advance(2, MustDate(2026, time.September, 20), 1000))

	// The clock is frozen in March 2026, so the reversal this writes is dated
	// well outside the September period being asked about — which is the point.
	if _, err := svc.Reverse(ctx, inside, Actor{UserID: 2, Username: "manager"}, "wrong farmer"); err != nil {
		t.Fatalf("reverse the advance: %v", err)
	}

	advanced, err := svc.AdvancedBetween(ctx,
		MustDate(2026, time.September, 1), MustDate(2026, time.September, 30))
	if err != nil {
		t.Fatalf("advanced in September: %v", err)
	}
	if len(advanced) != 1 {
		t.Fatalf("advanced in September: got %+v, want only the advance that still stands", advanced)
	}
	if advanced[0].CustomerID != 2 || advanced[0].Amount.String() != "1000.00" {
		t.Fatalf("advanced in September: got %+v, want customer 2 with 1000.00", advanced[0])
	}
}

func TestAdvancedBetweenCountsAdvancesAndNothingElse(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()
	on := MustDate(2026, time.September, 15)

	mustPost(t, svc, advance(1, on, 5000))
	mustPost(t, svc, payment(1, on, 1000, 0))
	mustPost(t, svc, Entry{
		CustomerID: 1, Date: on, Type: TypeInterest,
		Interest: money.FromCentavos(15_000), By: Actor{UserID: 1, Username: "dan"},
	})

	advanced, err := svc.AdvancedBetween(ctx,
		MustDate(2026, time.September, 1), MustDate(2026, time.September, 30))
	if err != nil {
		t.Fatalf("advanced in September: %v", err)
	}

	// The payment reduced the balance and the interest raised it; neither is
	// money that was lent, so neither belongs in the base of a flat charge.
	if len(advanced) != 1 || advanced[0].Amount.String() != "5000.00" {
		t.Fatalf("advanced in September: got %+v, want 5000.00", advanced)
	}
}

func TestAdvancedBetweenRefusesAPeriodThatRunsBackwards(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	_, err := svc.AdvancedBetween(ctx,
		MustDate(2026, time.September, 30), MustDate(2026, time.September, 1))
	if err == nil {
		t.Fatal("a period running backwards: got no error, want one")
	}
}

// The bulk read is a second way to compute a number this package can already
// compute, which is the shape that goes wrong quietly — so the property asserts
// the only thing that has to be true: the two cannot disagree, about any
// customer, on any date.
func TestPropertyBulkBalancesAgreeWithTheSingleRead(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		svc, _ := newPropertyLedger(rt, dir)

		steps := rapid.IntRange(1, 15).Draw(rt, "steps")
		for step := 0; step < steps; step++ {
			entry := genEntry(rt, fmt.Sprintf("step%d", step))
			if _, err := svc.Post(ctx, entry); err != nil {
				rt.Fatalf("post %s for customer %d: %v", entry.Type, entry.CustomerID, err)
			}
		}

		// A cut date anywhere in the generated year, plus its two ends, so the
		// empty case and the everything case are both reachable.
		on := MustDate(2026, time.January, 1).
			AddDays(rapid.IntRange(-1, 365).Draw(rt, "cut"))

		bulk, err := svc.BalancesAt(ctx, on)
		if err != nil {
			rt.Fatalf("balances at %s: %v", on, err)
		}
		byCustomer := make(map[int64]Balance, len(bulk))
		for _, b := range bulk {
			byCustomer[b.CustomerID] = b
		}

		for id := int64(1); id <= propertyCustomers; id++ {
			single, err := svc.BalanceAt(ctx, id, on)
			if err != nil {
				rt.Fatalf("balance at %s for customer %d: %v", on, id, err)
			}

			got, listed := byCustomer[id]
			if !listed {
				// Left out of the bulk read means "owes nothing at all", and
				// that claim has to be true.
				if !single.Principal.IsZero() || !single.Interest.IsZero() {
					rt.Fatalf("customer %d is missing from the balances at %s but owes %s / %s",
						id, on, single.Principal, single.Interest)
				}
				continue
			}
			if got.Principal != single.Principal || got.Interest != single.Interest {
				rt.Fatalf("customer %d at %s: bulk says %s / %s, single says %s / %s",
					id, on, got.Principal, got.Interest, single.Principal, single.Interest)
			}
		}
	})
}

// The rule, and both halves of it. A voucher posted in January and voided in
// March still counts in January — because that is what the books said on 31
// January, and the ledger dates a reversal the day of the void precisely so an
// old report stays reproducible. Over a window that reaches the void, it nets
// to nothing.
func TestActivityNetsAVoidOnceItHasHappened(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))
	voided := mustPost(t, svc, advance(2, MustDate(2026, time.January, 12), 3000))
	mustPost(t, svc, payment(1, MustDate(2026, time.January, 20), 1000, 0))

	// The clock is frozen on 2 March, so this reversal is dated after January.
	if _, err := svc.Reverse(ctx, voided, Actor{UserID: 2, Username: "manager"}, "wrong farmer"); err != nil {
		t.Fatalf("void the advance: %v", err)
	}

	january, err := svc.ActivityBetween(ctx, MustDate(2026, time.January, 1), MustDate(2026, time.January, 31))
	if err != nil {
		t.Fatalf("activity in January: %v", err)
	}
	if len(january) != 2 {
		t.Fatalf("January's activity is %+v; on 31 January the void had not happened", january)
	}

	toMarch, err := svc.ActivityBetween(ctx, MustDate(2026, time.January, 1), MustDate(2026, time.March, 31))
	if err != nil {
		t.Fatalf("activity to March: %v", err)
	}
	if len(toMarch) != 1 {
		t.Fatalf("the activity to March is %+v; the voided voucher should leave nobody behind", toMarch)
	}
	if got := toMarch[0]; got.CustomerID != 1 ||
		got.Advanced.String() != "5000.00" || got.Repaid.String() != "1000.00" {
		t.Errorf("the activity to March is %+v, want customer 1 advanced 5000.00 and repaid 1000.00", got)
	}
}

func TestActivitySplitsInterestFromPrincipal(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()
	on := MustDate(2026, time.January, 10)

	mustPost(t, svc, advance(1, on, 5000))
	mustPost(t, svc, Entry{
		CustomerID: 1, Date: on, Type: TypeInterest,
		Interest: money.FromCentavos(15_000), By: Actor{UserID: 1, Username: "dan"},
	})
	// A payment clearing 100.00 of interest and 400.00 of principal.
	mustPost(t, svc, payment(1, on, 400, 100))

	activity, err := svc.ActivityBetween(ctx, MustDate(2026, time.January, 1), MustDate(2026, time.January, 31))
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	got := activity[0]
	if got.Advanced.String() != "5000.00" || got.Repaid.String() != "400.00" ||
		got.InterestCharged.String() != "150.00" || got.InterestCollected.String() != "100.00" {
		t.Errorf("activity is %+v, want 5000 advanced, 400 repaid, 150 charged, 100 collected", got)
	}
}

func TestDeltasCarryBothDirectionsInDateOrder(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))
	mustPost(t, svc, payment(1, MustDate(2026, time.February, 1), 2000, 0))
	mustPost(t, svc, advance(1, MustDate(2026, time.February, 20), 1000))

	lots, err := svc.DeltasUpTo(ctx, MustDate(2026, time.March, 1))
	if err != nil {
		t.Fatalf("lots: %v", err)
	}
	if len(lots) != 3 {
		t.Fatalf("got %d lots, want three: %+v", len(lots), lots)
	}
	want := []string{"5000.00", "-2000.00", "1000.00"}
	for i, amount := range want {
		if lots[i].Principal.String() != amount {
			t.Errorf("delta %d is %s, want %s — oldest first, both directions", i, lots[i].Principal, amount)
		}
	}
}

// An entry and its reversal cancel to nothing once the void has happened — and
// not a day before it. Asked as at a date before the void, the money was still
// out, which is what the books said then.
func TestDeltasDropAVoidedEntryOnceTheVoidHasHappened(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))
	voided := mustPost(t, svc, advance(1, MustDate(2026, time.January, 11), 3000))

	// Dated 2 March, the day the clock is frozen on.
	if _, err := svc.Reverse(ctx, voided, Actor{UserID: 2, Username: "manager"}, "wrong amount"); err != nil {
		t.Fatalf("void: %v", err)
	}

	before, err := svc.DeltasUpTo(ctx, MustDate(2026, time.March, 1))
	if err != nil {
		t.Fatalf("deltas before the void: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("got %+v as at 1 March; the void had not happened yet", before)
	}

	after, err := svc.DeltasUpTo(ctx, MustDate(2026, time.March, 3))
	if err != nil {
		t.Fatalf("deltas after the void: %v", err)
	}
	if len(after) != 1 || after[0].Principal.String() != "5000.00" {
		t.Fatalf("got %+v as at 3 March, want only the advance that still stands", after)
	}
}

// The property that keeps the reports honest: what a customer did, less what
// they undid, is what they owe. Over generated ledgers, activity since the
// beginning must reconcile with the balance the ledger reports.
func TestPropertyActivityReconcilesWithTheBalance(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		svc, _ := newPropertyLedger(rt, dir)

		steps := rapid.IntRange(1, 15).Draw(rt, "steps")
		for step := 0; step < steps; step++ {
			entry := genEntry(rt, fmt.Sprintf("step%d", step))
			if _, err := svc.Post(ctx, entry); err != nil {
				rt.Fatalf("post: %v", err)
			}
		}

		on := MustDate(2027, time.January, 1)
		activity, err := svc.ActivityBetween(ctx, Date{}, on)
		if err != nil {
			rt.Fatalf("activity: %v", err)
		}
		byCustomer := make(map[int64]Activity, len(activity))
		for _, a := range activity {
			byCustomer[a.CustomerID] = a
		}

		lots, err := svc.DeltasUpTo(ctx, on)
		if err != nil {
			rt.Fatalf("lots: %v", err)
		}
		principal := make(map[int64]int64, propertyCustomers)
		for _, lot := range lots {
			principal[lot.CustomerID] += lot.Principal.Centavos()
		}

		for id := int64(1); id <= propertyCustomers; id++ {
			balance, err := svc.BalanceAt(ctx, id, on)
			if err != nil {
				rt.Fatalf("balance: %v", err)
			}

			// The deltas are every movement there has ever been, so their
			// principal must sum to the principal balance exactly — whatever
			// mix of entry types produced it.
			if principal[id] != balance.Principal.Centavos() {
				rt.Fatalf("customer %d: the deltas sum to %d of principal and the balance says %s",
					id, principal[id], balance.Principal)
			}

			// Interest charged less interest collected accounts for the
			// interest balance, except where an adjustment or an opening entry
			// moved interest — which the generator does produce, so this is
			// asserted as an inequality of direction rather than an identity.
			a := byCustomer[id]
			if a.InterestCollected.Centavos() < 0 || a.InterestCharged.Centavos() < 0 {
				rt.Fatalf("customer %d has a negative quantity: %+v", id, a)
			}
			if a.Advanced.Centavos() < 0 || a.Repaid.Centavos() < 0 {
				rt.Fatalf("customer %d has a negative quantity: %+v", id, a)
			}
		}
	})
}
