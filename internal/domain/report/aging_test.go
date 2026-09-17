package report

import (
	"testing"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// The FIFO walk is pure, so it is tested without a database. These are the
// cases somebody would have to reason about with a pencil and a calendar.

var asOf = ledger.MustDate(2026, time.June, 30)

// out is money going out n days before the report's date.
func out(daysAgo int, pesos int64) ledger.Delta {
	return ledger.Delta{
		CustomerID: 1,
		Date:       asOf.AddDays(-daysAgo),
		Principal:  mustPesos(pesos),
	}
}

// back is money coming back n days before the report's date.
func back(daysAgo int, pesos int64) ledger.Delta {
	amount, err := mustPesos(pesos).Neg()
	if err != nil {
		panic(err)
	}
	return ledger.Delta{CustomerID: 1, Date: asOf.AddDays(-daysAgo), Principal: amount}
}

func mustPesos(p int64) money.Money {
	m, err := money.FromPesos(p)
	if err != nil {
		panic(err)
	}
	return m
}

func TestAge(t *testing.T) {
	cases := []struct {
		name                            string
		deltas                          []ledger.Delta
		current, days60, days90, over90 string
	}{
		{
			"one advance, this month",
			[]ledger.Delta{out(10, 5000)},
			"5000.00", "0.00", "0.00", "0.00",
		},
		{
			"one advance in each bucket",
			[]ledger.Delta{out(5, 1000), out(45, 2000), out(75, 3000), out(200, 4000)},
			"1000.00", "2000.00", "3000.00", "4000.00",
		},

		// The boundaries, which are where an aging report is wrong if it is
		// wrong at all. Thirty days old is still current; thirty-one is not.
		{"exactly 30 days", []ledger.Delta{out(30, 1000)}, "1000.00", "0.00", "0.00", "0.00"},
		{"exactly 31 days", []ledger.Delta{out(31, 1000)}, "0.00", "1000.00", "0.00", "0.00"},
		{"exactly 60 days", []ledger.Delta{out(60, 1000)}, "0.00", "1000.00", "0.00", "0.00"},
		{"exactly 61 days", []ledger.Delta{out(61, 1000)}, "0.00", "0.00", "1000.00", "0.00"},
		{"exactly 90 days", []ledger.Delta{out(90, 1000)}, "0.00", "0.00", "1000.00", "0.00"},
		{"exactly 91 days", []ledger.Delta{out(91, 1000)}, "0.00", "0.00", "0.00", "1000.00"},

		// Payments go against the oldest money first, which is the whole point
		// of the report: it answers "how long has the oldest unpaid money been
		// out", not "how long ago was the last advance".
		{
			"a payment clears the oldest lot exactly",
			[]ledger.Delta{out(100, 1000), out(10, 2000), back(5, 1000)},
			"2000.00", "0.00", "0.00", "0.00",
		},
		{
			"a payment eats into the oldest lot",
			[]ledger.Delta{out(100, 1000), out(10, 2000), back(5, 400)},
			"2000.00", "0.00", "0.00", "600.00",
		},
		{
			"a payment spans two lots",
			[]ledger.Delta{out(100, 1000), out(45, 2000), back(5, 1500)},
			"0.00", "1500.00", "0.00", "0.00",
		},
		{
			"a payment clears everything",
			[]ledger.Delta{out(100, 1000), out(45, 2000), back(5, 3000)},
			"0.00", "0.00", "0.00", "0.00",
		},
		{
			"a payment larger than everything ever lent leaves no negative bucket",
			[]ledger.Delta{out(100, 1000), back(5, 4000)},
			"0.00", "0.00", "0.00", "0.00",
		},

		// Interest passes straight through: it is charged against a balance
		// rather than lent on a day, so it has no age to report.
		{
			"an interest charge is not aged",
			[]ledger.Delta{out(45, 1000), {CustomerID: 1, Date: asOf, Interest: mustPesos(30)}},
			"0.00", "1000.00", "0.00", "0.00",
		},

		// The counterexample the property test shrank to, at its own scale: an
		// overpayment's credit has to be carried, or the next advance ages in
		// full while the balance is net of it.
		{
			"an overpayment is spent on the next advance, not lost",
			[]ledger.Delta{out(50, 1000), back(40, 2000), out(10, 2000)},
			"1000.00", "0.00", "0.00", "0.00",
		},
		{
			"a credit larger than the next advance survives it",
			[]ledger.Delta{out(50, 1000), back(40, 5000), out(10, 2000)},
			"0.00", "0.00", "0.00", "0.00",
		},

		{"nothing at all", nil, "0.00", "0.00", "0.00", "0.00"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Age(tc.deltas, asOf)
			if err != nil {
				t.Fatalf("age: %v", err)
			}
			if got.Current.String() != tc.current || got.Days60.String() != tc.days60 ||
				got.Days90.String() != tc.days90 || got.Over90.String() != tc.over90 {
				t.Errorf("buckets are %s / %s / %s / %s, want %s / %s / %s / %s",
					got.Current, got.Days60, got.Days90, got.Over90,
					tc.current, tc.days60, tc.days90, tc.over90)
			}
			for _, bucket := range []money.Money{got.Current, got.Days60, got.Days90, got.Over90} {
				if bucket.IsNegative() {
					t.Errorf("a negative bucket: %s", bucket)
				}
			}
		})
	}
}

// An opening balance at cutover is outstanding principal that is already old,
// and it must age from its own date like everything else. Age never asks what
// kind of entry produced a movement, which is what makes this work without a
// special case.
func TestAgeDoesNotCareWhichEntryTypeMovedTheMoney(t *testing.T) {
	opening := ledger.Delta{CustomerID: 1, Date: asOf.AddDays(-200), Principal: mustPesos(8000)}

	got, err := Age([]ledger.Delta{opening}, asOf)
	if err != nil {
		t.Fatalf("age: %v", err)
	}
	if got.Over90.String() != "8000.00" {
		t.Errorf("an opening balance 200 days old aged to %s in the over-90 bucket, want 8000.00", got.Over90)
	}
}

func TestAgeNeedsADate(t *testing.T) {
	if _, err := Age([]ledger.Delta{out(10, 1000)}, ledger.Date{}); err == nil {
		t.Fatal("aging as at no date at all: got no error, want one")
	}
}
