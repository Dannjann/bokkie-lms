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

// ROIRow is one settlement in the period: the legacy Return of Investment
// table, row for row. Principal is what the settlement's payments returned of
// the money lent, Interest is what they paid of the interest charged, and
// Amount is the two together.
type ROIRow struct {
	Who
	SettlementID int64       `json:"settlement_id"`
	SettlementNo int64       `json:"settlement_no"`
	Cutoff       ledger.Date `json:"cutoff"`
	Principal    money.Money `json:"principal"`
	Interest     money.Money `json:"interest"`
	Amount       money.Money `json:"amount"`
}

// ROI is the portfolio over a period, with a row per settlement under it.
type ROI struct {
	From ledger.Date `json:"from"`
	To   ledger.Date `json:"to"`

	Deployed        money.Money `json:"deployed"`
	InterestCharged money.Money `json:"interest_charged"`
	Outstanding     money.Money `json:"outstanding"`

	// Repaid, InterestCollected and Returned are the totals of the rows: the
	// legacy report's "Return", "Paid" and "Total".
	Repaid            money.Money `json:"repaid"`
	InterestCollected money.Money `json:"interest_collected"`
	Returned          money.Money `json:"returned"`

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
// The table is the legacy report's (docs/plans/legacy-findings.md, Q3): every
// settlement whose cut-off falls in the period, with what its payments returned
// of principal and paid of interest. The block above it — what was lent, what
// interest was charged, what is still out — is the portfolio view Step 8 added,
// read from the ledger.
//
// The rows sum to Repaid, InterestCollected and Returned exactly, and that is
// the report's one real invariant.
func (s *Service) ROI(ctx context.Context, from, to ledger.Date) (ROI, error) {
	activity, err := s.ledger.ActivityBetween(ctx, from, to)
	if err != nil {
		return ROI{}, err
	}
	balances, err := s.ledger.BalancesAt(ctx, to)
	if err != nil {
		return ROI{}, err
	}
	settled, err := s.settlementsBetween(ctx, from, to)
	if err != nil {
		return ROI{}, err
	}

	report := ROI{From: from, To: to, Rows: make([]ROIRow, 0, len(settled))}
	for _, a := range activity {
		if report.Deployed, err = report.Deployed.Add(a.Advanced); err != nil {
			return ROI{}, fmt.Errorf("total what was deployed: %w", err)
		}
		if report.InterestCharged, err = report.InterestCharged.Add(a.InterestCharged); err != nil {
			return ROI{}, fmt.Errorf("total the interest charged: %w", err)
		}
	}

	farmers := map[int64]bool{}
	for _, st := range settled {
		amount, err := st.PaidPrincipal.Add(st.PaidInterest)
		if err != nil {
			return ROI{}, fmt.Errorf("total settlement %d: %w", st.No, err)
		}
		report.Rows = append(report.Rows, ROIRow{
			Who: st.Who, SettlementID: st.ID, SettlementNo: st.No, Cutoff: st.Cutoff,
			Principal: st.PaidPrincipal, Interest: st.PaidInterest, Amount: amount,
		})
		farmers[st.CustomerID] = true
		if report.Repaid, err = report.Repaid.Add(st.PaidPrincipal); err != nil {
			return ROI{}, fmt.Errorf("total what was repaid: %w", err)
		}
		if report.InterestCollected, err = report.InterestCollected.Add(st.PaidInterest); err != nil {
			return ROI{}, fmt.Errorf("total the interest collected: %w", err)
		}
		if report.Returned, err = report.Returned.Add(amount); err != nil {
			return ROI{}, fmt.Errorf("total what came back: %w", err)
		}
	}

	// Outstanding is the whole book at the end of the period, not anything the
	// rows add up to: a farmer who was not settled this period can still owe.
	for _, b := range balances {
		total, err := b.Total()
		if err != nil {
			return ROI{}, fmt.Errorf("total what customer %d owes: %w", b.CustomerID, err)
		}
		if report.Outstanding, err = report.Outstanding.Add(total); err != nil {
			return ROI{}, fmt.Errorf("total what is outstanding: %w", err)
		}
	}

	report.Farmers = len(farmers)
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

// --- Settled ---------------------------------------------------------------

// Settled is one settlement as the Settled report lists it.
type Settled struct {
	Who
	ID      int64       `json:"settlement_id"`
	No      int64       `json:"settlement_no"`
	Cutoff  ledger.Date `json:"cutoff"`
	RateBps int64       `json:"rate_bps"`

	InterestCharged money.Money `json:"interest_charged"`
	PaidPrincipal   money.Money `json:"paid_principal"`
	PaidInterest    money.Money `json:"paid_interest"`

	// BalanceAfter is what the farmer still owed once the settlement was
	// applied: the legacy "Present Balance". Negative when the payment was
	// worth more than the debt.
	BalanceAfter money.Money `json:"balance_after"`
}

// SettledReport is every settlement in a period.
type SettledReport struct {
	From ledger.Date `json:"from"`
	To   ledger.Date `json:"to"`
	Rows []Settled   `json:"rows"`
}

// Settled lists the settlements whose cut-off falls in the period, newest
// first. Each row opens the settlement, which is the legacy "Recall Liquidated
// Transaction": a settled account read back exactly as it was computed, and
// reprinted.
//
// "Liquidated" in the legacy system meant settled up on a date, not paid off —
// 561 of its 1,097 settlements carried a balance forward — which is why this
// report lists settlements rather than farmers whose balance reached zero.
func (s *Service) Settled(ctx context.Context, from, to ledger.Date) (SettledReport, error) {
	rows, err := s.settlementsBetween(ctx, from, to)
	if err != nil {
		return SettledReport{}, err
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].Cutoff.Equal(rows[j].Cutoff) {
			return rows[i].Cutoff.After(rows[j].Cutoff)
		}
		return rows[i].No > rows[j].No
	})
	return SettledReport{From: from, To: to, Rows: rows}, nil
}

// settlementsBetween reads the settlements whose cut-off falls in the period
// and which stood at the end of it: posted, or voided after the period ended.
// That is the ledger's rule restated — a void is dated the day it happened, so
// a report over an earlier period reads as the books read then.
func (s *Service) settlementsBetween(ctx context.Context, from, to ledger.Date) ([]Settled, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT st.id, st.settlement_no, st.customer_id, c.code, c.name, st.cutoff_date, st.rate_bps,
		       st.carried_charge_cents + COALESCE(a.charge, 0),
		       COALESCE(p.principal, 0), COALESCE(p.interest, 0),
		       st.carried_principal_cents + st.carried_interest_cents
		         + COALESCE(a.principal, 0) + st.carried_charge_cents + COALESCE(a.charge, 0)
		         - COALESCE(p.principal, 0) - COALESCE(p.interest, 0)
		  FROM settlements st
		  JOIN customers c ON c.id = st.customer_id
		  LEFT JOIN (SELECT settlement_id, SUM(principal_cents) AS principal, SUM(charge_cents) AS charge
		               FROM settlement_advances GROUP BY settlement_id) a ON a.settlement_id = st.id
		  LEFT JOIN (SELECT settlement_id, SUM(principal_cents) AS principal, SUM(interest_cents) AS interest
		               FROM settlement_payments GROUP BY settlement_id) p ON p.settlement_id = st.id
		 WHERE st.cutoff_date >= ? AND st.cutoff_date <= ?
		   AND (st.status = 'posted'
		        OR (st.status = 'voided' AND substr(st.voided_at, 1, 10) > ?))
		 ORDER BY c.code, st.cutoff_date, st.settlement_no`, from, to, to)
	if err != nil {
		return nil, fmt.Errorf("read the settlements between %s and %s: %w", from, to, err)
	}
	defer rows.Close()

	out := []Settled{}
	for rows.Next() {
		var r Settled
		if err := rows.Scan(&r.ID, &r.No, &r.CustomerID, &r.Code, &r.Name, &r.Cutoff, &r.RateBps,
			&r.InterestCharged, &r.PaidPrincipal, &r.PaidInterest, &r.BalanceAfter); err != nil {
			return nil, fmt.Errorf("read the settlements between %s and %s: %w", from, to, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// byCode sorts a report's rows the way staff read them: by the code they say
// out loud. Generic over the row type so every report sorts the same way rather
// than each one deciding.
func byCode[T any](rows []T, code func(T) string) {
	sort.SliceStable(rows, func(i, j int) bool { return code(rows[i]) < code(rows[j]) })
}
