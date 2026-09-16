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

// PostTx turns a draft into money received: it allocates the OR No. and writes
// the ledger entry, all inside the caller's transaction.
//
// **The UPDATE below must be the first statement this transaction runs**, and
// this is the first document in the system where that is not obviously
// possible. internal/db opens transactions deferred, so a transaction that
// reads first and writes second has to upgrade its lock — and SQLite refuses an
// upgrade that would deadlock immediately, without honouring busy_timeout. That
// is what made step 4's read-then-write allocator fail as SQLITE_BUSY on 43 of
// 50 concurrent writers, and what step 5's posting was shaped to avoid.
//
// A payment genuinely has to read: the split depends on what the customer owes
// right now. But the rule only requires that the *first* statement be a write,
// so the order is write, then read, then compute, then write:
//
//  1. UPDATE — the number, the status, who posted. The transaction is now a
//     writer, and a second poster waits its turn on the busy timeout.
//  2. Read the balance.
//  3. Split, in Go, over money.Money.
//  4. Post the entry.
//
// Everything that would normally be a precondition is therefore checked after
// the write instead: whether the payment exists, and whether it was still a
// draft. Each returns an error, the caller rolls back, and nothing happened.
func (s *Service) PostTx(ctx context.Context, tx *sql.Tx, id int64, by Actor) (Payment, error) {
	if by.Username == "" {
		return Payment{}, fmt.Errorf("a payment must say who posted it")
	}

	// The OR No. is allocated inside the UPDATE, never as a read followed by a
	// write, with the partial unique index on or_no as the backstop. MAX
	// ignores the NULLs that drafts carry, so the sequence counts posted
	// receipts only and has no gaps in it.
	//
	// orno.start therefore only has an effect while nothing has been posted:
	// after that MAX wins. That is deliberate — a setting that could renumber a
	// live sequence is a footgun — and it is why the legacy number goes in
	// before the first post at cutover.
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
		return Payment{}, fmt.Errorf("post payment %d: %w", id, err)
	}

	// Zero rows means it was not a draft — which is also what a double-clicked
	// Post button looks like, and why it posts once. A payment credited twice
	// is worse than a voucher posted twice: it is wrong in the direction
	// nobody complains about.
	if err := s.refused(ctx, tx, id, res, StatusDraft); err != nil {
		return Payment{}, err
	}

	p, err := byID(ctx, tx, id)
	if err != nil {
		return Payment{}, err
	}

	// Read inside the transaction, after the write. The balance a payment is
	// split against must be the one that is true at the moment the entry is
	// written, not one read before another poster's entry landed.
	owed, err := s.ledger.BalanceForTx(ctx, tx, p.CustomerID)
	if err != nil {
		return Payment{}, fmt.Errorf("read what customer %d owes: %w", p.CustomerID, err)
	}

	principal, interest, err := Split(p.Amount, owed, s.rule())
	if err != nil {
		return Payment{}, fmt.Errorf("post payment %d: %w", id, err)
	}

	entry, err := entryFor(p, principal, interest, by)
	if err != nil {
		return Payment{}, fmt.Errorf("post payment %d: %w", id, err)
	}
	if _, err := s.ledger.PostBatchTx(ctx, tx, []ledger.Entry{entry}); err != nil {
		return Payment{}, fmt.Errorf("post payment %d: %w", id, err)
	}
	return p, nil
}

// VoidTx undoes a posted payment by reversing the entry it produced.
//
// The status UPDATE goes first for the same reason as in PostTx, and it carries
// the same benefit: a double-clicked Void matches no rows the second time, long
// before the ledger's at-most-once reversal index is reached.
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

	entries, err := s.ledger.EntriesBySourceTx(ctx, tx, SourceTable, []int64{id})
	if err != nil {
		return Payment{}, fmt.Errorf("find what payment %d posted: %w", id, err)
	}

	// The document and the ledger must agree about what this payment did before
	// any of it is undone. A payment produces exactly one entry, so anything
	// else here is a record that has been interfered with, and reversing part
	// of it would be worse than refusing: it would look finished.
	var posted []ledger.Entry
	for _, e := range entries {
		if !e.IsReversal() {
			posted = append(posted, e)
		}
	}
	if len(posted) != 1 {
		return Payment{}, fmt.Errorf(
			"payment %d has %d entries in the ledger, not the one it wrote: refusing to void it",
			id, len(posted))
	}

	if _, err := s.ledger.ReverseTx(ctx, tx, posted[0].ID, by, reason); err != nil {
		return Payment{}, fmt.Errorf("reverse entry %d of payment %d: %w", posted[0].ID, id, err)
	}
	return p, nil
}

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

// entryFor is the ledger entry a payment becomes when it is posted.
//
// One entry, not two. An entry carries both deltas, and splitting a payment
// across two rows would make one event look like two on a statement.
//
// Both deltas are negated here: a payment reduces what is owed, and the ledger
// signs everything from the lender's point of view so that a balance is a plain
// SUM with no case analysis anywhere in the system.
func entryFor(p Payment, principal, interest money.Money, by Actor) (ledger.Entry, error) {
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
		Date:        p.Date,
		Type:        ledger.TypePayment,
		Principal:   principalDelta,
		Interest:    interestDelta,
		SourceTable: SourceTable,
		SourceID:    p.ID,
		By:          by,
	}, nil
}
