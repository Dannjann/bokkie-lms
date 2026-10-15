package payment

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// Split divides a payment between principal and interest.
//
// Pure, and exported, so that it can be tested exhaustively without a database
// and hammered directly by the property tests. Everything it needs is an
// argument: the amount, what the customer owed at that moment, and the rule.
//
// Three things hold under either rule, and they are the invariants the property
// tests assert:
//
//   - principal + interest == amount. The split divides a payment; it never
//     changes its size.
//   - Neither part is negative.
//   - Neither part exceeds what was owed on that component — except where the
//     payment exceeds the whole balance, in which case the excess lands on
//     principal and the customer is in credit.
//
// The clamp to zero in take() is not decoration. A customer already in credit
// has a negative balance on that component, and applying a negative amount to
// it would quietly charge them.
func Split(amount money.Money, owed ledger.Balance, rule Rule) (principal, interest money.Money, err error) {
	if !amount.IsPositive() {
		return money.Zero, money.Zero, fmt.Errorf("a payment of %s cannot be split", amount)
	}

	if rule == PrincipalFirst {
		principal = take(amount, owed.Principal)
		rest, err := amount.Sub(principal)
		if err != nil {
			return money.Zero, money.Zero, fmt.Errorf("split %s: %w", amount, err)
		}
		interest = take(rest, owed.Interest)

		// Whatever is left over is more than the farmer owed at all. It goes on
		// principal rather than interest under both rules, so that a customer in
		// credit is in credit on the principal they will next be advanced.
		excess, err := rest.Sub(interest)
		if err != nil {
			return money.Zero, money.Zero, fmt.Errorf("split %s: %w", amount, err)
		}
		if principal, err = principal.Add(excess); err != nil {
			return money.Zero, money.Zero, fmt.Errorf("split %s: %w", amount, err)
		}
		return principal, interest, nil
	}

	interest = take(amount, owed.Interest)
	if principal, err = amount.Sub(interest); err != nil {
		return money.Zero, money.Zero, fmt.Errorf("split %s: %w", amount, err)
	}
	return principal, interest, nil
}

// take is how much of amount this component can absorb: all of what is owed on
// it, or the whole amount if that is smaller, and nothing at all if the
// component is already in credit.
func take(amount, owed money.Money) money.Money {
	if !owed.IsPositive() {
		return money.Zero
	}
	if owed.Centavos() < amount.Centavos() {
		return owed
	}
	return amount
}

// PostTx records a payment: it allocates the OR No. and freezes the payment,
// inside the caller's transaction.
//
// **It does not write to the ledger.** A recorded payment waits for a
// settlement, which charges interest to its cut-off and then applies the
// payment interest first — the legacy rule, kept on purpose. Writing the entry
// here would apply the delivery to a balance that has not yet been charged the
// interest it is meant to clear first, and the split would come out different
// from the one the station has always made.
//
// The UPDATE is the only statement, so there is no lock upgrade to avoid; the
// rule that the first statement be a write holds trivially.
func (s *Service) PostTx(ctx context.Context, tx *sql.Tx, id int64, by Actor) (Payment, error) {
	if by.Username == "" {
		return Payment{}, fmt.Errorf("a payment must say who recorded it")
	}

	// The OR No. is allocated inside the UPDATE, never as a read followed by a
	// write, with the partial unique index on or_no as the backstop. MAX
	// ignores the NULLs that drafts carry, so the sequence counts posted
	// payments only and has no gaps in it.
	//
	// orno.start therefore only has an effect while nothing has been posted:
	// after that MAX wins. That is deliberate — a setting that could renumber a
	// live sequence is a footgun.
	start := int64(s.station.Int(settings.OrNoStart))
	if start < 1 {
		start = 1
	}

	stamp := s.now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`UPDATE payments
		    SET or_no = (SELECT COALESCE(MAX(or_no), ? - 1) + 1 FROM payments),
		        status = 'posted', posted_at = ?, posted_by = ?, posted_by_username = ?
		  WHERE id = ? AND status = 'draft'`,
		start, stamp, nullableID(by.UserID), by.Username, id)
	if err != nil {
		return Payment{}, fmt.Errorf("record payment %d: %w", id, err)
	}

	// Zero rows means it was not a draft — which is also what a double-clicked
	// button looks like, and why it records once.
	if err := s.refused(ctx, tx, id, res, StatusDraft); err != nil {
		return Payment{}, err
	}
	return byID(ctx, tx, id)
}

// VoidTx voids a recorded payment that no settlement has applied.
//
// A settled payment is refused with ErrSettled: its money is in the ledger as
// part of a settlement, and voiding it alone would leave the settlement
// describing a delivery that never came. Voiding the settlement reopens the
// payment, and then it can be voided here.
//
// An unsettled payment wrote nothing to the ledger, so normally there is
// nothing to reverse. Any entry still standing against it is reversed anyway —
// a payment recorded before settlements existed posted its own entry, and
// leaving that behind would be a void that refunds nothing.
func (s *Service) VoidTx(ctx context.Context, tx *sql.Tx, id int64, by Actor, reason string) (Payment, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Payment{}, ValidationError{Field: "reason", Message: "a void has to say why"}
	}
	if by.Username == "" {
		return Payment{}, fmt.Errorf("a void must say who made it")
	}

	stamp := s.now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`UPDATE payments
		    SET status = 'voided', voided_at = ?, voided_by = ?, voided_by_username = ?, void_reason = ?
		  WHERE id = ? AND status = 'posted'`,
		stamp, nullableID(by.UserID), by.Username, reason, id)
	if err != nil {
		return Payment{}, fmt.Errorf("void payment %d: %w", id, err)
	}
	if err := s.refused(ctx, tx, id, res, StatusPosted); err != nil {
		return Payment{}, err
	}

	p, err := byID(ctx, tx, id)
	if err != nil {
		return Payment{}, err
	}
	if p.SettlementID != 0 {
		return Payment{}, fmt.Errorf("%w: settlement %d applied payment %d", ErrSettled, p.SettlementNo, id)
	}

	entries, err := s.ledger.EntriesBySourceTx(ctx, tx, SourceTable, []int64{id})
	if err != nil {
		return Payment{}, fmt.Errorf("find what payment %d posted: %w", id, err)
	}
	reversed := map[int64]bool{}
	for _, e := range entries {
		if e.IsReversal() {
			reversed[e.ReversesID] = true
		}
	}
	for _, e := range entries {
		if e.IsReversal() || reversed[e.ID] {
			continue
		}
		if _, err := s.ledger.ReverseTx(ctx, tx, e.ID, by, reason); err != nil {
			return Payment{}, fmt.Errorf("reverse entry %d of payment %d: %w", e.ID, id, err)
		}
	}
	return p, nil
}

// UnsettledTx is every recorded payment for a customer, dated on or before
// upTo, that no posted settlement has applied — oldest first, which is the
// order a settlement applies them in.
func (s *Service) UnsettledTx(ctx context.Context, q queryExecer, customerID int64, upTo ledger.Date) ([]Payment, error) {
	rows, err := q.QueryContext(ctx, selectPayment+`
		 WHERE p.customer_id = ? AND p.status = 'posted' AND p.payment_date <= ?
		   AND st.id IS NULL
		 ORDER BY p.payment_date, p.or_no, p.id`, customerID, upTo)
	if err != nil {
		return nil, fmt.Errorf("read the unsettled payments of customer %d: %w", customerID, err)
	}
	var out []Payment
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("read the unsettled payments of customer %d: %w", customerID, err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	for i := range out {
		if out[i].Lines, err = linesOf(ctx, q, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Unsettled is UnsettledTx outside a transaction, for a screen.
func (s *Service) Unsettled(ctx context.Context, customerID int64, upTo ledger.Date) ([]Payment, error) {
	return s.UnsettledTx(ctx, s.db, customerID, upTo)
}

// Rule is the station's payment application order, for the settlement that
// applies payments.
func (s *Service) Rule() Rule { return s.rule() }

// Entries returns the ledger entries a posted payment produced — the one it
// wrote, and the reversal that voiding it added.
//
// This is what the receipt screen shows underneath a posted payment, and it is
// where the split between principal and interest is read from. The payments
// table does not hold that division: the entry does, and a second copy of a
// number is a number that can disagree.
func (s *Service) Entries(ctx context.Context, id int64) ([]ledger.Entry, error) {
	p, err := s.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.Status == StatusDraft {
		return nil, nil
	}
	return s.ledger.EntriesBySource(ctx, SourceTable, []int64{p.ID})
}

// EntryFor is the ledger entry a payment becomes when a settlement applies it.
//
// One entry, not two. An entry carries both deltas, and splitting a payment
// across two rows would make one event look like two on a statement.
//
// Both deltas are negated here: a payment reduces what is owed, and the ledger
// signs everything from the lender's point of view so that a balance is a plain
// SUM with no case analysis anywhere in the system.
func EntryFor(p Payment, on ledger.Date, principal, interest money.Money, by Actor) (ledger.Entry, error) {
	principalDelta, err := principal.Neg()
	if err != nil {
		return ledger.Entry{}, fmt.Errorf("negate the principal of payment %d: %w", p.ID, err)
	}
	interestDelta, err := interest.Neg()
	if err != nil {
		return ledger.Entry{}, fmt.Errorf("negate the interest of payment %d: %w", p.ID, err)
	}

	return ledger.Entry{
		CustomerID:  p.CustomerID,
		Date:        on,
		Type:        ledger.TypePayment,
		Principal:   principalDelta,
		Interest:    interestDelta,
		SourceTable: SourceTable,
		SourceID:    p.ID,
		By:          by,
	}, nil
}
