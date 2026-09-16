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
