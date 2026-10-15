package payment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CreateTx starts a draft payment.
//
// A draft is saved rather than held in the browser for the reason a voucher's
// is: this is typed at a counter, where a closed tab, a flat battery and a
// farmer who walks off mid-transaction all happen. It has no OR No. and has
// moved no money; until somebody posts it, this row is the whole of its
// existence.
//
// It is also what makes posting safe under a double-clicked button — see
// PostTx, where "still a draft" is the whole guard.
func (s *Service) CreateTx(ctx context.Context, tx *sql.Tx, d Draft, by Actor) (Payment, error) {
	draft, err := s.clean(d)
	if err != nil {
		return Payment{}, err
	}
	if by.Username == "" {
		return Payment{}, fmt.Errorf("a payment must say who started it")
	}
	if err := checkCustomer(ctx, tx, draft.CustomerID); err != nil {
		return Payment{}, err
	}

	stamp := s.now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`INSERT INTO payments
		     (customer_id, payment_date, amount_cents, method, particular, status,
		      created_at, created_by, created_by_username)
		 VALUES (?, ?, ?, ?, ?, 'draft', ?, ?, ?)`,
		draft.CustomerID, draft.Date, draft.Amount, string(draft.Method), draft.Particular,
		stamp, nullableID(by.UserID), by.Username)
	if err != nil {
		return Payment{}, fmt.Errorf("create a payment: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Payment{}, fmt.Errorf("create a payment: %w", err)
	}
	if err := writeLines(ctx, tx, id, draft.costed); err != nil {
		return Payment{}, err
	}
	return byID(ctx, tx, id)
}

// UpdateTx replaces a draft's contents.
//
// The UPDATE goes first, before anything is read. See PostTx for why that
// ordering is load-bearing rather than incidental.
func (s *Service) UpdateTx(ctx context.Context, tx *sql.Tx, id int64, d Draft) (Payment, error) {
	draft, err := s.clean(d)
	if err != nil {
		return Payment{}, err
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE payments
		    SET customer_id = ?, payment_date = ?, amount_cents = ?, method = ?, particular = ?
		  WHERE id = ? AND status = 'draft'`,
		draft.CustomerID, draft.Date, draft.Amount, string(draft.Method), draft.Particular, id)
	if err != nil {
		return Payment{}, fmt.Errorf("update payment %d: %w", id, err)
	}
	if err := s.refused(ctx, tx, id, res, StatusDraft); err != nil {
		return Payment{}, err
	}

	// After the write, so this read cannot be the lock upgrade PostTx exists to
	// avoid. A customer who does not exist rolls the whole thing back.
	if err := checkCustomer(ctx, tx, draft.CustomerID); err != nil {
		return Payment{}, err
	}
	if err := writeLines(ctx, tx, id, draft.costed); err != nil {
		return Payment{}, err
	}
	return byID(ctx, tx, id)
}

// DeleteTx removes a draft outright.
//
// Safe for the reason that makes it uninteresting: a draft moved no money. What
// it said is kept in the audit entry the route writes, which is where a deleted
// draft should be looked for.
func (s *Service) DeleteTx(ctx context.Context, tx *sql.Tx, id int64) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM payments WHERE id = ? AND status = 'draft'`, id)
	if err != nil {
		return fmt.Errorf("delete payment %d: %w", id, err)
	}
	return s.refused(ctx, tx, id, res, StatusDraft)
}

// refused turns "the statement matched no rows" into the sentence that explains
// why, which is either that the payment is not there or that it is no longer in
// the status the operation needs.
//
// It reads *after* the write that failed to match, never before it: by then the
// statement has already taken the write lock, so the read cannot be the lock
// upgrade PostTx exists to avoid.
func (s *Service) refused(ctx context.Context, q queryExecer, id int64, res sql.Result, want Status) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("payment %d: %w", id, err)
	}
	if affected > 0 {
		return nil
	}

	var status string
	switch err := q.QueryRowContext(ctx, `SELECT status FROM payments WHERE id = ?`, id).Scan(&status); {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: %d", ErrNotFound, id)
	case err != nil:
		return fmt.Errorf("read the status of payment %d: %w", id, err)
	}

	if want == StatusPosted {
		return fmt.Errorf("%w: payment %d is %s", ErrNotPosted, id, status)
	}
	return fmt.Errorf("%w: payment %d is %s", ErrNotDraft, id, status)
}

// ByID loads one payment.
func (s *Service) ByID(ctx context.Context, id int64) (Payment, error) {
	return byID(ctx, s.db, id)
}

// The customer's code and name travel with every payment because every screen
// that shows one shows them, and the alternative is a second round trip.
const selectPayment = `
	SELECT p.id, COALESCE(p.or_no, 0), p.customer_id, c.code, c.name,
	       p.payment_date, p.amount_cents, p.method, p.particular, p.status,
	       p.created_at, COALESCE(p.created_by, 0), p.created_by_username,
	       COALESCE(p.posted_at, ''), COALESCE(p.posted_by, 0), p.posted_by_username,
	       COALESCE(p.voided_at, ''), COALESCE(p.voided_by, 0), p.voided_by_username, p.void_reason,
	       COALESCE(st.id, 0), COALESCE(st.settlement_no, 0)
	  FROM payments p
	  JOIN customers c ON c.id = p.customer_id
	  LEFT JOIN settlement_payments sp ON sp.payment_id = p.id
	        AND sp.settlement_id IN (SELECT id FROM settlements WHERE status = 'posted')
	  LEFT JOIN settlements st ON st.id = sp.settlement_id`

func byID(ctx context.Context, q queryExecer, id int64) (Payment, error) {
	p, err := scanPayment(q.QueryRowContext(ctx, selectPayment+` WHERE p.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Payment{}, fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	if err != nil {
		return Payment{}, fmt.Errorf("read payment %d: %w", id, err)
	}
	if p.Lines, err = linesOf(ctx, q, id); err != nil {
		return Payment{}, err
	}
	return p, nil
}

// scanner is what *sql.Row and *sql.Rows have in common.
type scanner interface{ Scan(dest ...any) error }

func scanPayment(row scanner) (Payment, error) {
	var p Payment
	var method, status, createdAt, postedAt, voidedAt string
	err := row.Scan(&p.ID, &p.OrNo, &p.CustomerID, &p.CustomerCode, &p.CustomerName,
		&p.Date, &p.Amount, &method, &p.Particular, &status,
		&createdAt, &p.CreatedBy.UserID, &p.CreatedBy.Username,
		&postedAt, &p.PostedBy.UserID, &p.PostedBy.Username,
		&voidedAt, &p.VoidedBy.UserID, &p.VoidedBy.Username, &p.VoidReason,
		&p.SettlementID, &p.SettlementNo)
	if err != nil {
		return Payment{}, err
	}

	p.Method, p.Status = Method(method), Status(status)
	if p.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return Payment{}, fmt.Errorf("payment %d has an unreadable created_at %q: %w", p.ID, createdAt, err)
	}
	if p.PostedAt, err = optionalTime(postedAt); err != nil {
		return Payment{}, fmt.Errorf("payment %d has an unreadable posted_at %q: %w", p.ID, postedAt, err)
	}
	if p.VoidedAt, err = optionalTime(voidedAt); err != nil {
		return Payment{}, fmt.Errorf("payment %d has an unreadable voided_at %q: %w", p.ID, voidedAt, err)
	}
	return p, nil
}

// optionalTime reads a stamp that is absent until something has happened —
// posting, voiding — as nil rather than as the zero time, so a screen showing
// "posted at" has nothing to print rather than the year 1.
func optionalTime(text string) (*time.Time, error) {
	if text == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
