package advance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// CreateTx starts a draft voucher.
//
// A draft is saved rather than held in the browser because a voucher is typed
// at a counter, where a closed tab, a flat battery or a farmer who walks off
// mid-transaction all happen. It has no Ref. No. and has moved no money: until
// somebody posts it, this row and its lines are the whole of its existence.
func (s *Service) CreateTx(ctx context.Context, tx *sql.Tx, d Draft, by Actor) (Voucher, error) {
	draft, err := s.clean(d)
	if err != nil {
		return Voucher{}, err
	}
	if by.Username == "" {
		return Voucher{}, fmt.Errorf("a voucher must say who started it")
	}

	stamp := s.now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`INSERT INTO advance_vouchers (voucher_date, notes, status, created_at, created_by, created_by_username)
		 VALUES (?, ?, 'draft', ?, ?, ?)`,
		draft.Date, draft.Notes, stamp, nullableID(by.UserID), by.Username)
	if err != nil {
		return Voucher{}, fmt.Errorf("create a voucher: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Voucher{}, fmt.Errorf("create a voucher: %w", err)
	}

	if err := s.writeLines(ctx, tx, id, draft.Lines); err != nil {
		return Voucher{}, err
	}
	return byID(ctx, tx, id)
}

// UpdateTx replaces a draft's header and its lines.
//
// The lines are replaced wholesale rather than diffed. Nothing points at a
// draft's line — no ledger entry can, because a draft has produced none — so
// there is nothing a delete-and-reinsert can orphan, and the alternative is a
// diffing path that exists only to save writes on a table with a dozen rows.
//
// The UPDATE goes first, before anything is read. See postTx for why that
// ordering is load-bearing rather than incidental.
func (s *Service) UpdateTx(ctx context.Context, tx *sql.Tx, id int64, d Draft) (Voucher, error) {
	draft, err := s.clean(d)
	if err != nil {
		return Voucher{}, err
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE advance_vouchers SET voucher_date = ?, notes = ? WHERE id = ? AND status = 'draft'`,
		draft.Date, draft.Notes, id)
	if err != nil {
		return Voucher{}, fmt.Errorf("update voucher %d: %w", id, err)
	}
	if err := s.refused(ctx, tx, id, res, StatusDraft); err != nil {
		return Voucher{}, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM advance_lines WHERE voucher_id = ?`, id); err != nil {
		return Voucher{}, fmt.Errorf("clear the lines of voucher %d: %w", id, err)
	}
	if err := s.writeLines(ctx, tx, id, draft.Lines); err != nil {
		return Voucher{}, err
	}
	return byID(ctx, tx, id)
}

// DeleteTx removes a draft outright, lines and all.
//
// This is the one place in the system where something is genuinely deleted, and
// it is safe for the reason that makes it uninteresting: a draft moved no
// money. What it said is kept in the audit entry the route writes, which is
// where a deleted draft should be looked for.
func (s *Service) DeleteTx(ctx context.Context, tx *sql.Tx, id int64) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM advance_vouchers WHERE id = ? AND status = 'draft'`, id)
	if err != nil {
		return fmt.Errorf("delete voucher %d: %w", id, err)
	}
	return s.refused(ctx, tx, id, res, StatusDraft)
}

// refused turns "the statement matched no rows" into the sentence that explains
// why, which is either that the voucher is not there or that it is no longer in
// the status the operation needs.
//
// It reads *after* the write that failed to match, never before it: by then the
// statement has already taken the write lock, so the read cannot be the lock
// upgrade that postTx exists to avoid.
func (s *Service) refused(ctx context.Context, q queryExecer, id int64, res sql.Result, want Status) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("voucher %d: %w", id, err)
	}
	if affected > 0 {
		return nil
	}

	var status string
	switch err := q.QueryRowContext(ctx, `SELECT status FROM advance_vouchers WHERE id = ?`, id).Scan(&status); {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: %d", ErrNotFound, id)
	case err != nil:
		return fmt.Errorf("read the status of voucher %d: %w", id, err)
	}

	if want == StatusPosted {
		return fmt.Errorf("%w: voucher %d is %s", ErrNotPosted, id, status)
	}
	return fmt.Errorf("%w: voucher %d is %s", ErrNotDraft, id, status)
}

// writeLines inserts a draft's lines, after checking that every customer named
// is somebody the station can lend to.
//
// The check is a query rather than a reliance on the foreign key, because the
// foreign key cannot tell a customer who does not exist from one who has been
// deactivated, and those are two different sentences to read at a counter.
func (s *Service) writeLines(ctx context.Context, tx *sql.Tx, voucherID int64, lines []DraftLine) error {
	if err := checkCustomers(ctx, tx, lines); err != nil {
		return err
	}

	for i, line := range lines {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO advance_lines (voucher_id, customer_id, line_date, particular, amount_cents)
			 VALUES (?, ?, ?, ?, ?)`,
			voucherID, line.CustomerID, line.Date, line.Particular, line.Amount)
		if err != nil {
			return fmt.Errorf("write line %d of voucher %d: %w", i+1, voucherID, err)
		}
	}
	return nil
}

// checkCustomers reports the first line naming somebody who is not on the books
// or is no longer active.
//
// A deactivated customer cannot be added to a draft. A draft that already names
// one can still be posted — blocking that would strand a voucher that is
// correct, because deactivation is about who the station lends to today rather
// than about who owed money last week.
func checkCustomers(ctx context.Context, q queryExecer, lines []DraftLine) error {
	if len(lines) == 0 {
		return nil
	}

	ids := make([]any, 0, len(lines))
	marks := make([]string, 0, len(lines))
	seen := make(map[int64]bool, len(lines))
	for _, line := range lines {
		if seen[line.CustomerID] {
			continue
		}
		seen[line.CustomerID] = true
		ids = append(ids, line.CustomerID)
		marks = append(marks, "?")
	}

	rows, err := q.QueryContext(ctx,
		`SELECT id, active FROM customers WHERE id IN (`+strings.Join(marks, ", ")+`)`, ids...)
	if err != nil {
		return fmt.Errorf("check the customers on a voucher: %w", err)
	}
	defer rows.Close()

	active := make(map[int64]bool, len(ids))
	for rows.Next() {
		var id int64
		var isActive bool
		if err := rows.Scan(&id, &isActive); err != nil {
			return fmt.Errorf("check the customers on a voucher: %w", err)
		}
		active[id] = isActive
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("check the customers on a voucher: %w", err)
	}

	for i, line := range lines {
		isActive, known := active[line.CustomerID]
		switch {
		case !known:
			return ValidationError{
				Field:   fmt.Sprintf("lines[%d].customer_id", i),
				Message: fmt.Sprintf("line %d names a customer who is not on the books", i+1),
			}
		case !isActive:
			return ValidationError{
				Field:   fmt.Sprintf("lines[%d].customer_id", i),
				Message: fmt.Sprintf("line %d names a deactivated customer", i+1),
			}
		}
	}
	return nil
}

// ByID loads one voucher and its lines.
func (s *Service) ByID(ctx context.Context, id int64) (Voucher, error) {
	return byID(ctx, s.db, id)
}

const selectVoucher = `
	SELECT id, COALESCE(ref_no, 0), voucher_date, notes, status,
	       created_at, COALESCE(created_by, 0), created_by_username,
	       COALESCE(posted_at, ''), COALESCE(posted_by, 0), posted_by_username,
	       COALESCE(voided_at, ''), COALESCE(voided_by, 0), voided_by_username, void_reason
	  FROM advance_vouchers`

func byID(ctx context.Context, q queryExecer, id int64) (Voucher, error) {
	v, err := scanVoucher(q.QueryRowContext(ctx, selectVoucher+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Voucher{}, fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	if err != nil {
		return Voucher{}, fmt.Errorf("read voucher %d: %w", id, err)
	}

	if v.Lines, err = linesOf(ctx, q, id); err != nil {
		return Voucher{}, err
	}
	if v.Total, err = totalOf(v.Lines); err != nil {
		return Voucher{}, fmt.Errorf("total voucher %d: %w", id, err)
	}
	return v, nil
}

const selectLines = `
	SELECT l.id, l.customer_id, c.code, c.name, l.line_date, l.particular, l.amount_cents
	  FROM advance_lines l
	  JOIN customers c ON c.id = l.customer_id`

func linesOf(ctx context.Context, q queryExecer, voucherID int64) ([]Line, error) {
	// Ordered by id, which is the order they were typed. Nothing else about a
	// grid of lines has a defensible order, and a stable one matters because
	// this is what gets compared against a paper slip.
	rows, err := q.QueryContext(ctx, selectLines+` WHERE l.voucher_id = ? ORDER BY l.id`, voucherID)
	if err != nil {
		return nil, fmt.Errorf("read the lines of voucher %d: %w", voucherID, err)
	}
	defer rows.Close()

	// Empty rather than nil, so this cannot reach a browser as `null`. A
	// voucher with no lines is refused at the edge today, so this has never
	// fired — but it is the same one word that cost step 7 a blank screen.
	lines := []Line{}
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.ID, &l.CustomerID, &l.CustomerCode, &l.CustomerName,
			&l.Date, &l.Particular, &l.Amount); err != nil {
			return nil, fmt.Errorf("read a line of voucher %d: %w", voucherID, err)
		}
		lines = append(lines, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the lines of voucher %d: %w", voucherID, err)
	}
	return lines, nil
}

func totalOf(lines []Line) (money.Money, error) {
	amounts := make([]money.Money, 0, len(lines))
	for _, l := range lines {
		amounts = append(amounts, l.Amount)
	}
	return money.Sum(amounts...)
}

// scanner is what *sql.Row and *sql.Rows have in common.
type scanner interface{ Scan(dest ...any) error }

func scanVoucher(row scanner) (Voucher, error) {
	var v Voucher
	var status, createdAt, postedAt, voidedAt string
	err := row.Scan(&v.ID, &v.RefNo, &v.Date, &v.Notes, &status,
		&createdAt, &v.CreatedBy.UserID, &v.CreatedBy.Username,
		&postedAt, &v.PostedBy.UserID, &v.PostedBy.Username,
		&voidedAt, &v.VoidedBy.UserID, &v.VoidedBy.Username, &v.VoidReason)
	if err != nil {
		return Voucher{}, err
	}

	v.Status = Status(status)
	if v.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return Voucher{}, fmt.Errorf("voucher %d has an unreadable created_at %q: %w", v.ID, createdAt, err)
	}
	if v.PostedAt, err = optionalTime(postedAt); err != nil {
		return Voucher{}, fmt.Errorf("voucher %d has an unreadable posted_at %q: %w", v.ID, postedAt, err)
	}
	if v.VoidedAt, err = optionalTime(voidedAt); err != nil {
		return Voucher{}, fmt.Errorf("voucher %d has an unreadable voided_at %q: %w", v.ID, voidedAt, err)
	}
	return v, nil
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
