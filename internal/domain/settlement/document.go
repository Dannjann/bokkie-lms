package settlement

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
	"github.com/Dannjann/bokkie-lms/internal/domain/payment"
)

// CreateTx computes a draft settlement — the preview — and saves it.
func (s *Service) CreateTx(ctx context.Context, tx *sql.Tx, d Draft, by Actor) (Settlement, error) {
	draft, err := s.clean(d)
	if err != nil {
		return Settlement{}, err
	}
	if by.Username == "" {
		return Settlement{}, fmt.Errorf("a settlement must say who started it")
	}

	stamp := s.now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`INSERT INTO settlements (customer_id, cutoff_date, rate_bps, notes, status,
		                         created_at, created_by, created_by_username)
		 VALUES (?, ?, ?, ?, 'draft', ?, ?, ?)`,
		draft.CustomerID, draft.Cutoff, draft.RateBps, draft.Notes,
		stamp, nullableID(by.UserID), by.Username)
	if err != nil {
		return Settlement{}, fmt.Errorf("create a settlement: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Settlement{}, fmt.Errorf("create a settlement: %w", err)
	}

	if err := s.recompute(ctx, tx, id, draft); err != nil {
		return Settlement{}, err
	}
	return byID(ctx, tx, id)
}

// UpdateTx changes a draft's cut-off, rate or notes and recomputes it. Called
// with the same values, it is "recompute": the answer to a stale preview.
func (s *Service) UpdateTx(ctx context.Context, tx *sql.Tx, id int64, d Draft) (Settlement, error) {
	draft, err := s.clean(d)
	if err != nil {
		return Settlement{}, err
	}

	// The write goes first, as everywhere else: the transaction is a writer
	// before it reads anything.
	res, err := tx.ExecContext(ctx,
		`UPDATE settlements SET customer_id = ?, cutoff_date = ?, rate_bps = ?, notes = ?
		  WHERE id = ? AND status = 'draft'`,
		draft.CustomerID, draft.Cutoff, draft.RateBps, draft.Notes, id)
	if err != nil {
		return Settlement{}, fmt.Errorf("update settlement %d: %w", id, err)
	}
	if err := s.refused(ctx, tx, id, res, StatusDraft); err != nil {
		return Settlement{}, err
	}

	if err := s.recompute(ctx, tx, id, draft); err != nil {
		return Settlement{}, err
	}
	return byID(ctx, tx, id)
}

// recompute replaces a draft's figures with what the record says now.
func (s *Service) recompute(ctx context.Context, tx *sql.Tx, id int64, draft cleanDraft) error {
	c, err := s.compute(ctx, tx, draft.CustomerID, draft.Cutoff, draft.RateBps)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE settlements
		    SET carried_principal_cents = ?, carried_interest_cents = ?, carried_since = ?,
		        carried_days = ?, carried_charge_cents = ?
		  WHERE id = ? AND status = 'draft'`,
		c.Carried.Principal, c.Carried.Interest, nullableDate(c.Carried.Since),
		c.Carried.Days, c.Carried.Charge, id); err != nil {
		return fmt.Errorf("store what settlement %d carried: %w", id, err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM settlement_advances WHERE settlement_id = ?`, id); err != nil {
		return fmt.Errorf("clear settlement %d: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM settlement_payments WHERE settlement_id = ?`, id); err != nil {
		return fmt.Errorf("clear settlement %d: %w", id, err)
	}
	for _, a := range c.Advances {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settlement_advances
			     (settlement_id, advance_line_id, line_date, principal_cents, days, charge_cents)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			id, a.AdvanceLineID, a.Date, a.Principal, a.Days, a.Charge); err != nil {
			return fmt.Errorf("store an advance on settlement %d: %w", id, err)
		}
	}
	for _, p := range c.Payments {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settlement_payments (settlement_id, payment_id, principal_cents, interest_cents)
			 VALUES (?, ?, ?, ?)`,
			id, p.PaymentID, p.Principal, p.Interest); err != nil {
			return fmt.Errorf("store a payment on settlement %d: %w", id, err)
		}
	}
	return nil
}

// DeleteTx removes a draft. It wrote nothing, so there is nothing to undo.
func (s *Service) DeleteTx(ctx context.Context, tx *sql.Tx, id int64) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM settlements WHERE id = ? AND status = 'draft'`, id)
	if err != nil {
		return fmt.Errorf("delete settlement %d: %w", id, err)
	}
	return s.refused(ctx, tx, id, res, StatusDraft)
}

// ByID loads one settlement with its advances, payments and totals.
func (s *Service) ByID(ctx context.Context, id int64) (Settlement, error) {
	return byID(ctx, s.db, id)
}

const selectSettlement = `
	SELECT s.id, COALESCE(s.settlement_no, 0), s.customer_id, c.code, c.name,
	       s.cutoff_date, s.rate_bps, s.notes, s.status,
	       s.carried_principal_cents, s.carried_interest_cents, COALESCE(s.carried_since, ''),
	       s.carried_days, s.carried_charge_cents,
	       s.created_at, COALESCE(s.created_by, 0), s.created_by_username,
	       COALESCE(s.posted_at, ''), COALESCE(s.posted_by, 0), s.posted_by_username,
	       COALESCE(s.voided_at, ''), COALESCE(s.voided_by, 0), s.voided_by_username, s.void_reason
	  FROM settlements s
	  JOIN customers c ON c.id = s.customer_id`

func byID(ctx context.Context, q queryExecer, id int64) (Settlement, error) {
	var st Settlement
	var status, since, createdAt, postedAt, voidedAt string
	err := q.QueryRowContext(ctx, selectSettlement+` WHERE s.id = ?`, id).Scan(
		&st.ID, &st.No, &st.CustomerID, &st.CustomerCode, &st.CustomerName,
		&st.Cutoff, &st.RateBps, &st.Notes, &status,
		&st.Carried.Principal, &st.Carried.Interest, &since, &st.Carried.Days, &st.Carried.Charge,
		&createdAt, &st.CreatedBy.UserID, &st.CreatedBy.Username,
		&postedAt, &st.PostedBy.UserID, &st.PostedBy.Username,
		&voidedAt, &st.VoidedBy.UserID, &st.VoidedBy.Username, &st.VoidReason)
	if errors.Is(err, sql.ErrNoRows) {
		return Settlement{}, fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	if err != nil {
		return Settlement{}, fmt.Errorf("read settlement %d: %w", id, err)
	}

	st.Status = Status(status)
	if since != "" {
		if st.Carried.Since, err = ledger.ParseDate(since); err != nil {
			return Settlement{}, fmt.Errorf("settlement %d has an unreadable carried_since %q: %w", id, since, err)
		}
	}
	if st.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return Settlement{}, fmt.Errorf("settlement %d has an unreadable created_at: %w", id, err)
	}
	if st.PostedAt, err = optionalTime(postedAt); err != nil {
		return Settlement{}, fmt.Errorf("settlement %d has an unreadable posted_at: %w", id, err)
	}
	if st.VoidedAt, err = optionalTime(voidedAt); err != nil {
		return Settlement{}, fmt.Errorf("settlement %d has an unreadable voided_at: %w", id, err)
	}

	if st.Advances, err = advancesOf(ctx, q, id); err != nil {
		return Settlement{}, err
	}
	if st.Payments, err = paymentsOf(ctx, q, id); err != nil {
		return Settlement{}, err
	}
	if st.Totals, err = totals(st.computed()); err != nil {
		return Settlement{}, fmt.Errorf("settlement %d: %w", id, err)
	}
	return st, nil
}

// computed is the part of a stored settlement a post compares against a
// fresh computation.
func (st Settlement) computed() computed {
	return computed{Carried: st.Carried, Advances: st.Advances, Payments: st.Payments}
}

func advancesOf(ctx context.Context, q queryExecer, id int64) ([]Advance, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT sa.advance_line_id, v.id, COALESCE(v.ref_no, 0), sa.line_date, l.particular,
		        sa.principal_cents, sa.days, sa.charge_cents
		   FROM settlement_advances sa
		   JOIN advance_lines l ON l.id = sa.advance_line_id
		   JOIN advance_vouchers v ON v.id = l.voucher_id
		  WHERE sa.settlement_id = ?
		  ORDER BY sa.line_date, v.ref_no, l.id`, id)
	if err != nil {
		return nil, fmt.Errorf("read the advances of settlement %d: %w", id, err)
	}
	defer rows.Close()

	out := []Advance{}
	for rows.Next() {
		var a Advance
		if err := rows.Scan(&a.AdvanceLineID, &a.VoucherID, &a.RefNo, &a.Date, &a.Particular,
			&a.Principal, &a.Days, &a.Charge); err != nil {
			return nil, fmt.Errorf("read the advances of settlement %d: %w", id, err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func paymentsOf(ctx context.Context, q queryExecer, id int64) ([]Applied, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT sp.payment_id, COALESCE(p.or_no, 0), p.payment_date, p.method, p.amount_cents,
		        sp.principal_cents, sp.interest_cents
		   FROM settlement_payments sp
		   JOIN payments p ON p.id = sp.payment_id
		  WHERE sp.settlement_id = ?
		  ORDER BY p.payment_date, p.or_no, p.id`, id)
	if err != nil {
		return nil, fmt.Errorf("read the payments of settlement %d: %w", id, err)
	}
	defer rows.Close()

	out := []Applied{}
	for rows.Next() {
		var a Applied
		var method string
		if err := rows.Scan(&a.PaymentID, &a.OrNo, &a.Date, &method, &a.Amount,
			&a.Principal, &a.Interest); err != nil {
			return nil, fmt.Errorf("read the payments of settlement %d: %w", id, err)
		}
		a.Method = payment.Method(method)
		out = append(out, a)
	}
	return out, rows.Err()
}

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

// --- List ------------------------------------------------------------------

const (
	DefaultPerPage = 50
	MaxPerPage     = 200
)

// Query is a settlement search. From and To bound the cut-off date.
type Query struct {
	Text       string
	CustomerID int64
	Status     Status
	From, To   ledger.Date
	Page       int
	PerPage    int
}

// Summary is one settlement as a list shows it.
type Summary struct {
	ID           int64       `json:"id"`
	No           int64       `json:"settlement_no"`
	CustomerID   int64       `json:"customer_id"`
	CustomerCode string      `json:"customer_code"`
	CustomerName string      `json:"customer_name"`
	Cutoff       ledger.Date `json:"cutoff"`
	RateBps      int64       `json:"rate_bps"`
	Interest     money.Money `json:"interest"`
	Paid         money.Money `json:"paid"`
	Status       Status      `json:"status"`
	CreatedBy    string      `json:"created_by"`
}

// Page is one page of settlements.
type Page struct {
	Settlements []Summary `json:"settlements"`
	Total       int       `json:"total"`
	Page        int       `json:"page"`
	PerPage     int       `json:"per_page"`
}

func (q Query) normalise() Query {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PerPage < 1 {
		q.PerPage = DefaultPerPage
	}
	if q.PerPage > MaxPerPage {
		q.PerPage = MaxPerPage
	}
	q.Text = strings.TrimSpace(q.Text)
	return q
}

// List is a page of settlements, newest cut-off first.
func (s *Service) List(ctx context.Context, q Query) (Page, error) {
	q = q.normalise()
	where, args := q.condition()
	page := Page{Settlements: []Summary{}, Page: q.Page, PerPage: q.PerPage}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM settlements s JOIN customers c ON c.id = s.customer_id `+where,
		args...).Scan(&page.Total); err != nil {
		return Page{}, fmt.Errorf("count settlements: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id, COALESCE(s.settlement_no, 0), s.customer_id, c.code, c.name,
		        s.cutoff_date, s.rate_bps,
		        s.carried_charge_cents + COALESCE((SELECT SUM(charge_cents) FROM settlement_advances
		                                            WHERE settlement_id = s.id), 0),
		        COALESCE((SELECT SUM(principal_cents + interest_cents) FROM settlement_payments
		                   WHERE settlement_id = s.id), 0),
		        s.status, s.created_by_username
		   FROM settlements s
		   JOIN customers c ON c.id = s.customer_id
		   `+where+`
		  ORDER BY s.cutoff_date DESC, s.id DESC
		  LIMIT ? OFFSET ?`,
		append(args, q.PerPage, (q.Page-1)*q.PerPage)...)
	if err != nil {
		return Page{}, fmt.Errorf("list settlements: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sum Summary
		var status string
		if err := rows.Scan(&sum.ID, &sum.No, &sum.CustomerID, &sum.CustomerCode, &sum.CustomerName,
			&sum.Cutoff, &sum.RateBps, &sum.Interest, &sum.Paid, &status, &sum.CreatedBy); err != nil {
			return Page{}, fmt.Errorf("list settlements: %w", err)
		}
		sum.Status = Status(status)
		page.Settlements = append(page.Settlements, sum)
	}
	return page, rows.Err()
}

func (q Query) condition() (string, []any) {
	var clauses []string
	var args []any

	if q.CustomerID > 0 {
		clauses = append(clauses, "s.customer_id = ?")
		args = append(args, q.CustomerID)
	}
	if q.Status != "" {
		clauses = append(clauses, "s.status = ?")
		args = append(args, string(q.Status))
	}
	if !q.From.IsZero() {
		clauses = append(clauses, "s.cutoff_date >= ?")
		args = append(args, q.From)
	}
	if !q.To.IsZero() {
		clauses = append(clauses, "s.cutoff_date <= ?")
		args = append(args, q.To)
	}
	if q.Text != "" {
		text := "%" + likeEscaper.Replace(q.Text) + "%"
		theCustomer := `(c.code LIKE ? ESCAPE '\' OR c.name LIKE ? ESCAPE '\')`
		if no, err := strconv.ParseInt(q.Text, 10, 64); err == nil {
			clauses = append(clauses, "(s.settlement_no = ? OR "+theCustomer+")")
			args = append(args, no, text, text)
		} else {
			clauses = append(clauses, theCustomer)
			args = append(args, text, text)
		}
	}

	if len(clauses) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// NumbersFor maps settlement ids to their numbers, for the statement's
// "Settlement No. 12" label. Drafts have no number and are absent.
func (s *Service) NumbersFor(ctx context.Context, ids []int64) (map[int64]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, settlement_no FROM settlements WHERE id IN (`+marks+`) AND settlement_no IS NOT NULL`, args...)
	if err != nil {
		return nil, fmt.Errorf("read settlement numbers: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]int64, len(ids))
	for rows.Next() {
		var id, no int64
		if err := rows.Scan(&id, &no); err != nil {
			return nil, fmt.Errorf("read settlement numbers: %w", err)
		}
		out[id] = no
	}
	return out, rows.Err()
}
