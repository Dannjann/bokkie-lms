package settlement

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
	"github.com/Dannjann/bokkie-lms/internal/domain/payment"
)

// computed is everything a settlement works out from the record. It is what a
// draft stores and what a post recomputes and compares.
type computed struct {
	Carried  Carried
	Advances []Advance
	Payments []Applied
}

// compute works a settlement out from the record as it stands inside q.
//
//  1. The farmer's latest posted settlement fixes where this one starts. A
//     cut-off before it is refused: the record up to there is settled.
//  2. The open advances are the posted advance-slip lines for this farmer,
//     dated on or before the cut-off, that no posted settlement has charged.
//     Each is charged for the days from its own date to the cut-off.
//  3. The carried principal is the principal balance at the cut-off less the
//     open advances. It accrues from the previous cut-off, or from the
//     farmer's first entry when there has been no settlement — at cutover,
//     the opening balance.
//  4. The recorded payments not yet settled, dated on or before the cut-off,
//     are applied oldest first against what is then owed, by the station's
//     rule — interest first unless somebody has changed it.
func (s *Service) compute(ctx context.Context, q queryExecer, customerID int64, cutoff ledger.Date, rateBps int64) (computed, error) {
	if err := checkCustomer(ctx, q, customerID); err != nil {
		return computed{}, err
	}

	last, err := lastCutoff(ctx, q, customerID)
	if err != nil {
		return computed{}, err
	}
	if !last.IsZero() && cutoff.Before(last) {
		return computed{}, ValidationError{
			Field:   "cutoff",
			Message: fmt.Sprintf("this farmer is already liquidated up to %s — a cut-off cannot go back before it", last),
		}
	}

	advances, err := openAdvances(ctx, q, customerID, cutoff)
	if err != nil {
		return computed{}, err
	}
	open := money.Zero
	for i := range advances {
		a := &advances[i]
		a.Days = cutoff.DaysSince(a.Date)
		if a.Charge, err = Charge(a.Principal, rateBps, a.Days); err != nil {
			return computed{}, err
		}
		if open, err = open.Add(a.Principal); err != nil {
			return computed{}, fmt.Errorf("add up the open advances: %w", err)
		}
	}

	balance, err := s.balanceAt(ctx, q, customerID, cutoff)
	if err != nil {
		return computed{}, err
	}

	var carried Carried
	if carried.Principal, err = balance.Principal.Sub(open); err != nil {
		return computed{}, fmt.Errorf("work out the carried principal: %w", err)
	}
	carried.Interest = balance.Interest

	if carried.Principal.IsPositive() {
		carried.Since = last
		if carried.Since.IsZero() {
			if carried.Since, err = s.ledger.FirstEntryDateTx(ctx, q, customerID); err != nil {
				return computed{}, err
			}
		}
		if !carried.Since.IsZero() && carried.Since.Before(cutoff) {
			carried.Days = cutoff.DaysSince(carried.Since)
		}
		if carried.Charge, err = Charge(carried.Principal, rateBps, carried.Days); err != nil {
			return computed{}, err
		}
	}

	// What is owed once this settlement's interest is charged, which is what
	// the payments are applied against.
	charged := carried.Charge
	for _, a := range advances {
		if charged, err = charged.Add(a.Charge); err != nil {
			return computed{}, fmt.Errorf("add up the interest: %w", err)
		}
	}
	owed := ledger.Balance{CustomerID: customerID, Principal: balance.Principal}
	if owed.Interest, err = balance.Interest.Add(charged); err != nil {
		return computed{}, fmt.Errorf("add up the interest owed: %w", err)
	}

	unsettled, err := s.payments.UnsettledTx(ctx, q, customerID, cutoff)
	if err != nil {
		return computed{}, err
	}
	rule := s.payments.Rule()
	applied := make([]Applied, 0, len(unsettled))
	for _, p := range unsettled {
		principal, interest, err := payment.Split(p.Amount, owed, rule)
		if err != nil {
			return computed{}, fmt.Errorf("apply OR No. %d: %w", p.OrNo, err)
		}
		if owed.Principal, err = owed.Principal.Sub(principal); err != nil {
			return computed{}, fmt.Errorf("apply OR No. %d: %w", p.OrNo, err)
		}
		if owed.Interest, err = owed.Interest.Sub(interest); err != nil {
			return computed{}, fmt.Errorf("apply OR No. %d: %w", p.OrNo, err)
		}
		applied = append(applied, Applied{
			PaymentID: p.ID, OrNo: p.OrNo, Date: p.Date, Method: p.Method,
			Amount: p.Amount, Principal: principal, Interest: interest,
		})
	}

	return computed{Carried: carried, Advances: advances, Payments: applied}, nil
}

// balanceAt reads the ledger inside q when q is a transaction, so a post
// computes against the balance as it stands at the moment it writes.
func (s *Service) balanceAt(ctx context.Context, q queryExecer, customerID int64, on ledger.Date) (ledger.Balance, error) {
	if tx, ok := q.(*sql.Tx); ok {
		return s.ledger.BalanceAtTx(ctx, tx, customerID, on)
	}
	return s.ledger.BalanceAt(ctx, customerID, on)
}

// lastCutoff is the cut-off of the farmer's latest posted settlement, or the
// zero Date if they have never been settled.
func lastCutoff(ctx context.Context, q queryExecer, customerID int64) (ledger.Date, error) {
	var text string
	if err := q.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(cutoff_date), '') FROM settlements
		  WHERE customer_id = ? AND status = 'posted'`, customerID).Scan(&text); err != nil {
		return ledger.Date{}, fmt.Errorf("read the last settlement of customer %d: %w", customerID, err)
	}
	if text == "" {
		return ledger.Date{}, nil
	}
	return ledger.ParseDate(text)
}

// openAdvances is every posted advance-slip line for the farmer, dated on or
// before the cut-off, that no posted settlement has charged — the legacy
// "Empty(PDate)" — oldest first.
func openAdvances(ctx context.Context, q queryExecer, customerID int64, cutoff ledger.Date) ([]Advance, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT l.id, v.id, v.ref_no, l.line_date, l.particular, l.amount_cents
		   FROM advance_lines l
		   JOIN advance_vouchers v ON v.id = l.voucher_id
		  WHERE v.status = 'posted' AND l.customer_id = ? AND l.line_date <= ?
		    AND NOT EXISTS (SELECT 1
		                      FROM settlement_advances sa
		                      JOIN settlements st ON st.id = sa.settlement_id
		                     WHERE sa.advance_line_id = l.id AND st.status = 'posted')
		  ORDER BY l.line_date, v.ref_no, l.id`, customerID, cutoff)
	if err != nil {
		return nil, fmt.Errorf("read the open advances of customer %d: %w", customerID, err)
	}
	defer rows.Close()

	out := []Advance{}
	for rows.Next() {
		var a Advance
		if err := rows.Scan(&a.AdvanceLineID, &a.VoucherID, &a.RefNo, &a.Date, &a.Particular, &a.Principal); err != nil {
			return nil, fmt.Errorf("read the open advances of customer %d: %w", customerID, err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func checkCustomer(ctx context.Context, q queryExecer, customerID int64) error {
	var exists int
	switch err := q.QueryRowContext(ctx, `SELECT 1 FROM customers WHERE id = ?`, customerID).Scan(&exists); {
	case errors.Is(err, sql.ErrNoRows):
		return ValidationError{Field: "customer_id", Message: "that farmer is not on the books"}
	case err != nil:
		return fmt.Errorf("check the farmer on a settlement: %w", err)
	}
	return nil
}

// totals adds a settlement up.
func totals(c computed) (Totals, error) {
	var t Totals
	var err error
	add := func(dst *money.Money, v money.Money) {
		if err != nil {
			return
		}
		*dst, err = dst.Add(v)
	}

	for _, a := range c.Advances {
		add(&t.Advanced, a.Principal)
		add(&t.AdvanceInterest, a.Charge)
	}
	add(&t.InterestCharged, c.Carried.Charge)
	add(&t.InterestCharged, t.AdvanceInterest)

	add(&t.Principal, c.Carried.Principal)
	add(&t.Principal, t.Advanced)
	add(&t.Interest, c.Carried.Interest)
	add(&t.Interest, t.InterestCharged)
	add(&t.Owed, t.Principal)
	add(&t.Owed, t.Interest)

	for _, p := range c.Payments {
		add(&t.Paid, p.Amount)
		add(&t.PaidPrincipal, p.Principal)
		add(&t.PaidInterest, p.Interest)
	}
	if err != nil {
		return Totals{}, fmt.Errorf("add up a settlement: %w", err)
	}

	if t.PrincipalAfter, err = t.Principal.Sub(t.PaidPrincipal); err != nil {
		return Totals{}, err
	}
	if t.InterestAfter, err = t.Interest.Sub(t.PaidInterest); err != nil {
		return Totals{}, err
	}
	if t.BalanceAfter, err = t.PrincipalAfter.Add(t.InterestAfter); err != nil {
		return Totals{}, err
	}
	return t, nil
}
