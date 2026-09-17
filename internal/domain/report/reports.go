package report

import (
	"context"
	"fmt"
	"sort"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// --- Current Advances ------------------------------------------------------

// Debtor is one farmer's standing on the day the report is as at.
type Debtor struct {
	Who
	Principal    money.Money `json:"principal"`
	Interest     money.Money `json:"interest"`
	Total        money.Money `json:"total"`
	LastActivity ledger.Date `json:"last_activity"`
}

// CurrentAdvances is who owes what, as at a day.
type CurrentAdvances struct {
	AsOf ledger.Date `json:"as_of"`
	Rows []Debtor    `json:"rows"`

	Principal money.Money `json:"principal"`
	Interest  money.Money `json:"interest"`
	Total     money.Money `json:"total"`
}

// CurrentAdvances lists every farmer carrying a balance on a given day.
//
// A farmer in credit appears, with a negative balance, rather than being
// dropped. A credit is money the station owes attention to, and hiding it would
// make the total on this page disagree with the ledger — which is the one thing
// a report must never do.
func (s *Service) CurrentAdvances(ctx context.Context, asOf ledger.Date) (CurrentAdvances, error) {
	balances, err := s.ledger.BalancesAt(ctx, asOf)
	if err != nil {
		return CurrentAdvances{}, err
	}

	ids := make([]int64, 0, len(balances))
	for _, b := range balances {
		ids = append(ids, b.CustomerID)
	}
	who, err := s.names(ctx, ids)
	if err != nil {
		return CurrentAdvances{}, err
	}

	report := CurrentAdvances{AsOf: asOf, Rows: make([]Debtor, 0, len(balances))}
	for _, b := range balances {
		total, err := b.Total()
		if err != nil {
			return CurrentAdvances{}, fmt.Errorf("total what customer %d owes: %w", b.CustomerID, err)
		}
		report.Rows = append(report.Rows, Debtor{
			Who:          who[b.CustomerID],
			Principal:    b.Principal,
			Interest:     b.Interest,
			Total:        total,
			LastActivity: b.LastActivity,
		})
	}
	byCode(report.Rows, func(d Debtor) string { return d.Code })

	for _, row := range report.Rows {
		if report.Principal, err = report.Principal.Add(row.Principal); err != nil {
			return CurrentAdvances{}, fmt.Errorf("total the principal: %w", err)
		}
		if report.Interest, err = report.Interest.Add(row.Interest); err != nil {
			return CurrentAdvances{}, fmt.Errorf("total the interest: %w", err)
		}
	}
	if report.Total, err = sumMoney(report.Principal, report.Interest); err != nil {
		return CurrentAdvances{}, fmt.Errorf("total the report: %w", err)
	}
	return report, nil
}

// --- Return of Investment Status -------------------------------------------

// ROIRow is one farmer's part in a period.
type ROIRow struct {
	Who
	Advanced        money.Money `json:"advanced"`
	Repaid          money.Money `json:"repaid"`
	InterestCharged money.Money `json:"interest_charged"`

	// Balance is what they ended the period owing, which is not derivable from
	// the three figures beside it: a farmer can enter a period already owing
	// money and leave it owing more than the period alone explains.
	Balance money.Money `json:"balance"`
}

// ROI is the portfolio over a period, with a row per farmer under it.
type ROI struct {
	From ledger.Date `json:"from"`
	To   ledger.Date `json:"to"`

	Deployed          money.Money `json:"deployed"`
	Repaid            money.Money `json:"repaid"`
	InterestCharged   money.Money `json:"interest_charged"`
	InterestCollected money.Money `json:"interest_collected"`
	Outstanding       money.Money `json:"outstanding"`

	// Farmers and Vouchers say how much work the figures above represent.
	Farmers  int `json:"farmers"`
	Vouchers int `json:"vouchers"`

	// RepaidShare is Repaid as a percentage of Deployed, to one decimal place,
	// computed in integers and sent as text. Empty when nothing was deployed.
	RepaidShare string `json:"repaid_share"`

	Rows []ROIRow `json:"rows"`
}

// ROI builds the Return of Investment Status report.
//
// **This is Q3 answered by assumption** — see 08-reports.md. The totals answer
// whether the lending is working and the rows answer who, and the two are the
// same query at two groupings, which is what made building both cheaper than
// guessing between them.
//
// The rows sum to the totals above them exactly, and that is the report's one
// real invariant. Everything else on the page is a count or a percentage of two
// figures that are themselves sums.
func (s *Service) ROI(ctx context.Context, from, to ledger.Date) (ROI, error) {
	activity, err := s.ledger.ActivityBetween(ctx, from, to)
	if err != nil {
		return ROI{}, err
	}

	// The balance at the end of the period, which is a different question from
	// what moved during it.
	balances, err := s.ledger.BalancesAt(ctx, to)
	if err != nil {
		return ROI{}, err
	}
	closing := make(map[int64]money.Money, len(balances))
	for _, b := range balances {
		total, err := b.Total()
		if err != nil {
			return ROI{}, fmt.Errorf("total what customer %d owes: %w", b.CustomerID, err)
		}
		closing[b.CustomerID] = total
	}

	ids := make([]int64, 0, len(activity))
	for _, a := range activity {
		ids = append(ids, a.CustomerID)
	}
	who, err := s.names(ctx, ids)
	if err != nil {
		return ROI{}, err
	}

	report := ROI{From: from, To: to, Rows: make([]ROIRow, 0, len(activity))}
	for _, a := range activity {
		report.Rows = append(report.Rows, ROIRow{
			Who:             who[a.CustomerID],
			Advanced:        a.Advanced,
			Repaid:          a.Repaid,
			InterestCharged: a.InterestCharged,
			Balance:         closing[a.CustomerID],
		})

		if report.Deployed, err = report.Deployed.Add(a.Advanced); err != nil {
			return ROI{}, fmt.Errorf("total what was deployed: %w", err)
		}
		if report.Repaid, err = report.Repaid.Add(a.Repaid); err != nil {
			return ROI{}, fmt.Errorf("total what was repaid: %w", err)
		}
		if report.InterestCharged, err = report.InterestCharged.Add(a.InterestCharged); err != nil {
			return ROI{}, fmt.Errorf("total the interest charged: %w", err)
		}
		if report.InterestCollected, err = report.InterestCollected.Add(a.InterestCollected); err != nil {
			return ROI{}, fmt.Errorf("total the interest collected: %w", err)
		}
	}
	byCode(report.Rows, func(r ROIRow) string { return r.Code })

	// Outstanding is the whole book at the end of the period, not the sum of
	// the rows above: a farmer who did nothing this period can still owe money,
	// and a report that said otherwise would understate the book.
	for _, b := range balances {
		if report.Outstanding, err = report.Outstanding.Add(closing[b.CustomerID]); err != nil {
			return ROI{}, fmt.Errorf("total what is outstanding: %w", err)
		}
	}

	report.Farmers = len(activity)
	report.RepaidShare = share(report.Repaid, report.Deployed)

	if report.Vouchers, err = s.vouchersBetween(ctx, from, to); err != nil {
		return ROI{}, err
	}
	return report, nil
}

// vouchersBetween counts the vouchers posted in a period and still standing.
//
// This is the one figure on any report that the ledger cannot answer, because
// "how many vouchers" is a question about documents. Asking it here is the
// boundary working as intended: the report may know that vouchers exist, and
// the ledger still may not.
func (s *Service) vouchersBetween(ctx context.Context, from, to ledger.Date) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM advance_vouchers
		 WHERE status = 'posted' AND voucher_date >= ? AND voucher_date <= ?`,
		from, to).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count the vouchers between %s and %s: %w", from, to, err)
	}
	return count, nil
}

// --- Aging -----------------------------------------------------------------

// AgedRow is one farmer's outstanding principal by age, with their unpaid
// interest beside it rather than in it.
type AgedRow struct {
	Who
	Buckets
	Principal money.Money `json:"principal"`
	Interest  money.Money `json:"interest"`
}

// Aging is the whole book by how old the money is.
type Aging struct {
	AsOf ledger.Date `json:"as_of"`
	Rows []AgedRow   `json:"rows"`

	Buckets
	Principal money.Money `json:"principal"`
	Interest  money.Money `json:"interest"`
}

// Aging builds the aging report.
//
// One read of every movement up to the date, walked per customer by the pure
// FIFO function in aging.go. Nothing is stored, so nothing can drift out of
// step with the entries — which is the whole reason Step 6 refused an
// allocations table.
func (s *Service) Aging(ctx context.Context, asOf ledger.Date) (Aging, error) {
	deltas, err := s.ledger.DeltasUpTo(ctx, asOf)
	if err != nil {
		return Aging{}, err
	}
	balances, err := s.ledger.BalancesAt(ctx, asOf)
	if err != nil {
		return Aging{}, err
	}

	interest := make(map[int64]money.Money, len(balances))
	for _, b := range balances {
		interest[b.CustomerID] = b.Interest
	}

	grouped := byCustomer(deltas)
	ids := make([]int64, 0, len(grouped))
	for id := range grouped {
		ids = append(ids, id)
	}
	who, err := s.names(ctx, ids)
	if err != nil {
		return Aging{}, err
	}

	report := Aging{AsOf: asOf, Rows: make([]AgedRow, 0, len(grouped))}
	for _, id := range ids {
		buckets, err := Age(grouped[id], asOf)
		if err != nil {
			return Aging{}, fmt.Errorf("age what customer %d owes: %w", id, err)
		}
		principal, err := buckets.Total()
		if err != nil {
			return Aging{}, fmt.Errorf("total the buckets of customer %d: %w", id, err)
		}

		// A farmer with nothing outstanding and no unpaid interest is not on an
		// aging report: every column would be zero, and three hundred such rows
		// would bury the dozen that matter.
		if !principal.IsPositive() && !interest[id].IsPositive() {
			continue
		}

		report.Rows = append(report.Rows, AgedRow{
			Who:       who[id],
			Buckets:   buckets,
			Principal: principal,
			Interest:  interest[id],
		})
	}
	byCode(report.Rows, func(r AgedRow) string { return r.Code })

	for _, row := range report.Rows {
		if report.Current, err = report.Current.Add(row.Current); err != nil {
			return Aging{}, fmt.Errorf("total the current bucket: %w", err)
		}
		if report.Days60, err = report.Days60.Add(row.Days60); err != nil {
			return Aging{}, fmt.Errorf("total the 60-day bucket: %w", err)
		}
		if report.Days90, err = report.Days90.Add(row.Days90); err != nil {
			return Aging{}, fmt.Errorf("total the 90-day bucket: %w", err)
		}
		if report.Over90, err = report.Over90.Add(row.Over90); err != nil {
			return Aging{}, fmt.Errorf("total the over-90 bucket: %w", err)
		}
		if report.Interest, err = report.Interest.Add(row.Interest); err != nil {
			return Aging{}, fmt.Errorf("total the interest: %w", err)
		}
	}
	if report.Principal, err = report.Buckets.Total(); err != nil {
		return Aging{}, fmt.Errorf("total the buckets: %w", err)
	}
	return report, nil
}

// --- Liquidated ------------------------------------------------------------

// Settlement is one farmer who finished paying, and the day they did it.
type Settlement struct {
	Who
	SettledOn ledger.Date `json:"settled_on"`

	// Advanced is what they had been lent in total before they settled, which
	// is what makes the row worth reading: "paid off ₱45,000.00 on 12 March"
	// says something, "settled" alone does not.
	Advanced money.Money `json:"advanced"`
}

// Liquidated is who finished paying during a period.
type Liquidated struct {
	From ledger.Date  `json:"from"`
	To   ledger.Date  `json:"to"`
	Rows []Settlement `json:"rows"`
}

// Liquidated lists the farmers whose balance reached zero during the period and
// stayed there.
//
// **This is Q4 answered by assumption**, and the assumption is expressed as the
// absence of a write path rather than as a decision somebody has to remember.
//
// "Reached zero and stayed there" excludes the three hundred farmers who have
// simply never borrowed: a balance has to have been positive before it can have
// been settled. The day reported is the day of the entry that took them to
// zero, which is the one a farmer would recognise.
func (s *Service) Liquidated(ctx context.Context, from, to ledger.Date) (Liquidated, error) {
	deltas, err := s.ledger.DeltasUpTo(ctx, to)
	if err != nil {
		return Liquidated{}, err
	}

	grouped := byCustomer(deltas)
	report := Liquidated{From: from, To: to, Rows: []Settlement{}}

	settled := make(map[int64]Settlement)
	for id, movements := range grouped {
		var running, advanced money.Money
		var owedBefore bool
		var settledOn ledger.Date

		for _, d := range movements {
			if d.Principal.IsPositive() {
				if advanced, err = advanced.Add(d.Principal); err != nil {
					return Liquidated{}, fmt.Errorf("total what customer %d was lent: %w", id, err)
				}
			}

			delta, err := d.Total()
			if err != nil {
				return Liquidated{}, fmt.Errorf("total a movement of customer %d: %w", id, err)
			}
			if running, err = running.Add(delta); err != nil {
				return Liquidated{}, fmt.Errorf("run the balance of customer %d: %w", id, err)
			}

			switch {
			case running.IsPositive():
				// Back on the books: whatever settlement came before no longer
				// stands, and the next one has to be earned again.
				owedBefore = true
				settledOn = ledger.Date{}
			case owedBefore && settledOn.IsZero():
				// This is the entry that cleared them.
				settledOn = d.Date
			}
		}

		// Still at or below zero at the end of the period, and the day they got
		// there falls inside it.
		if settledOn.IsZero() || running.IsPositive() {
			continue
		}
		if settledOn.Before(from) || settledOn.After(to) {
			continue
		}
		settled[id] = Settlement{SettledOn: settledOn, Advanced: advanced}
	}

	ids := make([]int64, 0, len(settled))
	for id := range settled {
		ids = append(ids, id)
	}
	who, err := s.names(ctx, ids)
	if err != nil {
		return Liquidated{}, err
	}
	for id, s := range settled {
		s.Who = who[id]
		report.Rows = append(report.Rows, s)
	}

	// Most recent settlement first: the question this page is opened with is
	// "who has finished lately".
	sort.Slice(report.Rows, func(i, j int) bool {
		if report.Rows[i].SettledOn != report.Rows[j].SettledOn {
			return report.Rows[i].SettledOn.After(report.Rows[j].SettledOn)
		}
		return report.Rows[i].Code < report.Rows[j].Code
	})
	return report, nil
}

// byCode sorts a report's rows the way staff read them: by the code they say
// out loud. Generic over the row type so every report sorts the same way rather
// than each one deciding.
func byCode[T any](rows []T, code func(T) string) {
	sort.SliceStable(rows, func(i, j int) bool { return code(rows[i]) < code(rows[j]) })
}
