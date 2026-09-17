package report

import (
	"fmt"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// Aging: how old is the money that is still out.
//
// Derived at read time and stored nowhere. Step 6 refused a payment_allocations
// table because it would be a second source of truth beside "a balance is the
// sum of the entries", and one that voiding a document would then have to
// unwind. So the FIFO walk below happens when somebody opens the report, over
// the entries as they stand at that moment, and writes nothing.

// Buckets is outstanding principal by how long it has been out.
//
// Four, because that is what a lender reads: this month, last month, the month
// before, and the money that has stopped behaving like a loan.
type Buckets struct {
	Current money.Money `json:"current"` // 0–30 days
	Days60  money.Money `json:"days_60"` // 31–60
	Days90  money.Money `json:"days_90"` // 61–90
	Over90  money.Money `json:"over_90"` // more than 90
}

// Total is everything still out, whatever its age.
func (b Buckets) Total() (money.Money, error) {
	return money.Sum(b.Current, b.Days60, b.Days90, b.Over90)
}

// bucketFor decides where an amount that went out on a given day belongs, as at
// a date.
//
// The boundaries are inclusive at the young end — money exactly 30 days old is
// still "current" — and the arithmetic is on whole days between two calendar
// dates, so nothing here depends on a clock or a timezone. An entry dated in the
// future of the report's date ages as zero rather than as a negative, which is
// the only sane reading of a figure that cannot exist yet.
func bucketFor(days int) int {
	switch {
	case days <= 30:
		return 0
	case days <= 60:
		return 1
	case days <= 90:
		return 2
	default:
		return 3
	}
}

// Age applies what came back against what went out, oldest first, and reports
// what is left by age.
//
// Pure, exported, and taking a plain list so the property tests can hammer it
// without a database. The list is one customer's movements in date order, as
// ledger.DeltasUpTo returns them: **a positive principal is money that went out
// on that day, a negative one is money that came back.** Nothing here asks what
// kind of entry produced either, which is what makes an opening balance at
// cutover age correctly and stops a new entry type in some later step falling
// silently outside the report.
//
// Payments are applied to the oldest outstanding money first. That is a
// convention rather than a fact — no payment in this system names a voucher,
// and Step 6 decided deliberately that none ever would — but it is the
// convention every lender's aging uses, and it is the one that makes the report
// answer the question it is opened for: how long has the oldest unpaid money
// been out.
//
// A customer who has repaid more than they were ever lent leaves nothing in any
// bucket rather than a negative one: a credit is not aged debt, and it stays
// visible on Current Advances where it belongs.
//
// **But the credit is carried, not discarded.** A farmer who overpays and then
// borrows again has already paid for part of the new advance, and aging the
// whole of it would put more money in the buckets than the ledger says is
// outstanding. The property test found this at one centavo — 0.01 owed and 0.02
// aged — which is exactly the size of error nobody would ever notice on a
// printed page.
func Age(deltas []ledger.Delta, on ledger.Date) (Buckets, error) {
	if on.IsZero() {
		return Buckets{}, fmt.Errorf("aging needs a date to be as at")
	}

	// Outstanding lots, oldest first: each one is money that went out on a day
	// and has not yet been paid back.
	type lot struct {
		date   ledger.Date
		amount money.Money
	}
	var lots []lot

	// Money repaid beyond everything outstanding at the time. It waits here for
	// the next advance rather than being thrown away.
	credit := money.Zero

	for _, d := range deltas {
		switch {
		case d.Principal.IsZero():
			// An interest charge or an interest-only payment. Interest is not
			// aged — see the package's report — so it passes through.
			continue

		case d.Principal.IsPositive():
			amount := d.Principal

			// Anything already paid in advance is spent on this lot first.
			if credit.IsPositive() {
				if credit.Centavos() >= amount.Centavos() {
					remaining, err := credit.Sub(amount)
					if err != nil {
						return Buckets{}, fmt.Errorf("spend a credit of %s: %w", credit, err)
					}
					credit = remaining
					continue
				}
				reduced, err := amount.Sub(credit)
				if err != nil {
					return Buckets{}, fmt.Errorf("spend a credit of %s: %w", credit, err)
				}
				amount, credit = reduced, money.Zero
			}

			lots = append(lots, lot{date: d.Date, amount: amount})

		default:
			// Money coming back, applied to the oldest lot first. The amount is
			// negated once, here, so everything below is a positive quantity.
			paid, err := d.Principal.Neg()
			if err != nil {
				return Buckets{}, fmt.Errorf("apply a payment of %s: %w", d.Principal, err)
			}

			for len(lots) > 0 && paid.IsPositive() {
				oldest := lots[0]
				if oldest.amount.Centavos() > paid.Centavos() {
					// The payment is swallowed by the oldest lot, which
					// survives smaller.
					remaining, err := oldest.amount.Sub(paid)
					if err != nil {
						return Buckets{}, fmt.Errorf("reduce a lot of %s: %w", oldest.amount, err)
					}
					lots[0].amount = remaining
					paid = money.Zero
					break
				}

				// The lot is cleared, and whatever is left over goes on to the
				// next one.
				leftover, err := paid.Sub(oldest.amount)
				if err != nil {
					return Buckets{}, fmt.Errorf("clear a lot of %s: %w", oldest.amount, err)
				}
				paid = leftover
				lots = lots[1:]
			}

			// Anything still in hand is money repaid beyond everything
			// outstanding. It is a credit rather than negative debt, so it goes
			// into no bucket — and is kept for the next advance, which is what
			// makes the buckets add up to what the ledger says is owed.
			if paid.IsPositive() {
				carried, err := credit.Add(paid)
				if err != nil {
					return Buckets{}, fmt.Errorf("carry a credit of %s: %w", paid, err)
				}
				credit = carried
			}
		}
	}

	var buckets [4]money.Money
	for _, l := range lots {
		days := on.DaysSince(l.date)
		if days < 0 {
			days = 0
		}
		sum, err := buckets[bucketFor(days)].Add(l.amount)
		if err != nil {
			return Buckets{}, fmt.Errorf("total a bucket: %w", err)
		}
		buckets[bucketFor(days)] = sum
	}

	return Buckets{
		Current: buckets[0],
		Days60:  buckets[1],
		Days90:  buckets[2],
		Over90:  buckets[3],
	}, nil
}
