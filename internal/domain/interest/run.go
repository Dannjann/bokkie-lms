package interest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// charge is one computed line before it is a row: who, on what, how much.
type charge struct {
	CustomerID int64
	Base       money.Money
	Charge     money.Money
}

// CreateTx computes a run and saves it as a draft.
//
// This is the preview. It is stored rather than returned and thrown away
// because the thing that gets posted must be the thing that was looked at —
// see the package comment. It is also why there is no separate "preview"
// endpoint anywhere above this: there is nothing for one to call.
func (s *Service) CreateTx(ctx context.Context, tx *sql.Tx, d Draft, by Actor) (Run, error) {
	draft, err := s.clean(d)
	if err != nil {
		return Run{}, err
	}
	if by.Username == "" {
		return Run{}, fmt.Errorf("an interest run must say who started it")
	}

	// The rule is read here, once, and copied onto the row. Every later read of
	// this run uses the copy.
	rule, err := RuleFor(s.station)
	if err != nil {
		return Run{}, err
	}

	stamp := s.now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`INSERT INTO interest_runs
		        (period_from, period_to, method, rate_bps, basis, notes, status,
		         created_at, created_by, created_by_username)
		 VALUES (?, ?, ?, ?, ?, ?, 'draft', ?, ?, ?)`,
		draft.From, draft.To, rule.Method, rule.RateBps, rule.Basis, draft.Notes,
		stamp, nullableID(by.UserID), by.Username)
	if err != nil {
		return Run{}, fmt.Errorf("create an interest run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Run{}, fmt.Errorf("create an interest run: %w", err)
	}

	if err := s.computeInto(ctx, tx, id, rule, draft.From, draft.To); err != nil {
		return Run{}, err
	}
	return byID(ctx, tx, id)
}

// UpdateTx recomputes a draft — against a different period, or against the same
// one after the world has moved.
//
// The rule is read again, so changing the rate in Settings and recomputing is
// how a manager corrects a run made under the wrong one. Lines are replaced
// wholesale rather than diffed, as advance.UpdateTx replaces a voucher's:
// nothing points at a draft's line, because a draft has produced no entries.
//
// The UPDATE goes first, before anything is read. See PostTx for why that
// ordering is load-bearing rather than incidental.
func (s *Service) UpdateTx(ctx context.Context, tx *sql.Tx, id int64, d Draft) (Run, error) {
	draft, err := s.clean(d)
	if err != nil {
		return Run{}, err
	}
	rule, err := RuleFor(s.station)
	if err != nil {
		return Run{}, err
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE interest_runs
		    SET period_from = ?, period_to = ?, method = ?, rate_bps = ?, basis = ?, notes = ?
		  WHERE id = ? AND status = 'draft'`,
		draft.From, draft.To, rule.Method, rule.RateBps, rule.Basis, draft.Notes, id)
	if err != nil {
		return Run{}, fmt.Errorf("recompute interest run %d: %w", id, err)
	}
	if err := s.refused(ctx, tx, id, res, StatusDraft); err != nil {
		return Run{}, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM interest_run_lines WHERE run_id = ?`, id); err != nil {
		return Run{}, fmt.Errorf("clear the lines of interest run %d: %w", id, err)
	}
	if err := s.computeInto(ctx, tx, id, rule, draft.From, draft.To); err != nil {
		return Run{}, err
	}
	return byID(ctx, tx, id)
}

// DeleteTx removes a draft outright, lines and all.
//
// Safe for the reason that makes it uninteresting: a draft charged nothing.
// What it said is kept in the audit entry the route writes.
func (s *Service) DeleteTx(ctx context.Context, tx *sql.Tx, id int64) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM interest_runs WHERE id = ? AND status = 'draft'`, id)
	if err != nil {
		return fmt.Errorf("delete interest run %d: %w", id, err)
	}
	return s.refused(ctx, tx, id, res, StatusDraft)
}

// computeInto computes a run's lines and writes them.
func (s *Service) computeInto(ctx context.Context, tx *sql.Tx, runID int64, rule Rule, from, to ledger.Date) error {
	charges, err := s.compute(ctx, tx, rule, from, to)
	if err != nil {
		return err
	}
	for _, c := range charges {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO interest_run_lines (run_id, customer_id, base_cents, charge_cents)
			 VALUES (?, ?, ?, ?)`,
			runID, c.CustomerID, c.Base, c.Charge)
		if err != nil {
			return fmt.Errorf("write the line for customer %d of interest run %d: %w", c.CustomerID, runID, err)
		}
	}
	return nil
}

// compute is what a run charges, and it is the same function whether it is
// producing a preview or checking one at the moment of posting. That is the
// point: a preview computed by different code than the post is a second
// implementation that will disagree eventually.
//
// tx may be nil, in which case the ledger is read outside any transaction. It
// is never nil on the posting path, where the figures must be the ones true
// inside the transaction doing the writing.
//
// Lines come back ordered by customer id, which is the order the ledger's bulk
// reads return and the order the comparison at posting time relies on.
func (s *Service) compute(ctx context.Context, tx *sql.Tx, rule Rule, from, to ledger.Date) ([]charge, error) {
	if err := rule.Validate(); err != nil {
		return nil, err
	}

	bases, err := s.basesFor(ctx, tx, rule, from, to)
	if err != nil {
		return nil, err
	}

	charges := make([]charge, 0, len(bases))
	for _, base := range bases {
		amount, err := Charge(base.Amount, rule)
		if err != nil {
			return nil, fmt.Errorf("charge customer %d: %w", base.CustomerID, err)
		}

		// A charge that rounds to nothing is not a line. A farmer owing ₱1.00
		// at 3% is charged three centavos; one owing ₱0.10 is charged nothing,
		// and an entry that moves no money is refused by the ledger anyway — so
		// a line the ledger would refuse has no business being previewed as
		// though it would post.
		if !amount.IsPositive() {
			continue
		}
		charges = append(charges, charge{CustomerID: base.CustomerID, Base: base.Amount, Charge: amount})
	}
	return charges, nil
}

// basesFor is what each customer's charge is computed from, and it is the one
// place the three methods differ.
func (s *Service) basesFor(ctx context.Context, tx *sql.Tx, rule Rule, from, to ledger.Date) ([]ledger.CustomerAmount, error) {
	switch rule.Method {
	case MethodMonthlyOnBalance:
		var balances []ledger.Balance
		var err error
		if tx != nil {
			balances, err = s.ledger.BalancesAtTx(ctx, tx, to)
		} else {
			balances, err = s.ledger.BalancesAt(ctx, to)
		}
		if err != nil {
			return nil, fmt.Errorf("read what is owed as at %s: %w", to, err)
		}

		bases := make([]ledger.CustomerAmount, 0, len(balances))
		for _, b := range balances {
			base := b.Principal
			if rule.Basis == BasisPrincipalAndInterest {
				// Compounding: interest already charged and not yet paid is
				// itself charged on.
				if base, err = b.Total(); err != nil {
					return nil, fmt.Errorf("total what customer %d owes: %w", b.CustomerID, err)
				}
			}
			bases = append(bases, ledger.CustomerAmount{CustomerID: b.CustomerID, Amount: base})
		}
		return bases, nil

	case MethodFlatOnAdvance:
		// Basis does not apply here and is deliberately not consulted: a fee on
		// what was lent is charged on what was lent. The column still carries
		// whatever the setting said, because the run records the whole rule it
		// ran under rather than the parts of it that mattered.
		if tx != nil {
			return s.ledger.AdvancedBetweenTx(ctx, tx, from, to)
		}
		return s.ledger.AdvancedBetween(ctx, from, to)

	default:
		return nil, ValidationError{Field: "method", Message: fmt.Sprintf("%q is not a way interest is computed here", rule.Method)}
	}
}

// ByID loads one run and its lines.
func (s *Service) ByID(ctx context.Context, id int64) (Run, error) {
	return byID(ctx, s.db, id)
}

const selectRun = `
	SELECT id, COALESCE(run_no, 0), period_from, period_to, method, rate_bps, basis, notes, status,
	       created_at, COALESCE(created_by, 0), created_by_username,
	       COALESCE(posted_at, ''), COALESCE(posted_by, 0), posted_by_username,
	       COALESCE(voided_at, ''), COALESCE(voided_by, 0), voided_by_username, void_reason
	  FROM interest_runs`

func byID(ctx context.Context, q queryExecer, id int64) (Run, error) {
	run, err := scanRun(q.QueryRowContext(ctx, selectRun+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	if err != nil {
		return Run{}, fmt.Errorf("read interest run %d: %w", id, err)
	}

	if run.Lines, err = linesOf(ctx, q, id); err != nil {
		return Run{}, err
	}
	if run.Total, err = totalOf(run.Lines); err != nil {
		return Run{}, fmt.Errorf("total interest run %d: %w", id, err)
	}
	return run, nil
}

const selectLines = `
	SELECT l.id, l.customer_id, c.code, c.name, l.base_cents, l.charge_cents
	  FROM interest_run_lines l
	  JOIN customers c ON c.id = l.customer_id`

func linesOf(ctx context.Context, q queryExecer, runID int64) ([]Line, error) {
	// Ordered by customer id, which is the order they were computed in and the
	// order the recomputation at posting time compares against. A screen sorts
	// by code, which for this system is the same order.
	rows, err := q.QueryContext(ctx, selectLines+` WHERE l.run_id = ? ORDER BY l.customer_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("read the lines of interest run %d: %w", runID, err)
	}
	defer rows.Close()

	// Empty rather than nil, and this is not a style preference: a nil slice
	// marshals as `null`, a run that charges nobody has no lines, and the
	// screen reading `run.lines.length` off a null took the whole component
	// down — leaving the shell wrapped around a blank middle, from a run that
	// was behaving perfectly correctly. A collection on the wire is a list,
	// empty or not.
	lines := []Line{}
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.ID, &l.CustomerID, &l.CustomerCode, &l.CustomerName, &l.Base, &l.Charge); err != nil {
			return nil, fmt.Errorf("read a line of interest run %d: %w", runID, err)
		}
		lines = append(lines, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the lines of interest run %d: %w", runID, err)
	}
	return lines, nil
}

func totalOf(lines []Line) (money.Money, error) {
	amounts := make([]money.Money, 0, len(lines))
	for _, l := range lines {
		amounts = append(amounts, l.Charge)
	}
	return money.Sum(amounts...)
}

// RunNosForLines maps line ids to the run numbers they belong to.
//
// This is what turns an entry's provenance into "Interest run 3" on a
// statement. It lives here for the reason advance.RefNosForLines lives in that
// package: the ledger is not allowed to know this table exists, so the
// labelling happens above both.
func (s *Service) RunNosForLines(ctx context.Context, lineIDs []int64) (map[int64]int64, error) {
	if len(lineIDs) == 0 {
		return nil, nil
	}

	marks := make([]string, 0, len(lineIDs))
	args := make([]any, 0, len(lineIDs))
	for _, id := range lineIDs {
		marks = append(marks, "?")
		args = append(args, id)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT l.id, COALESCE(r.run_no, 0)
		   FROM interest_run_lines l
		   JOIN interest_runs r ON r.id = l.run_id
		  WHERE l.id IN (`+strings.Join(marks, ", ")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("read the run numbers behind %d lines: %w", len(lineIDs), err)
	}
	defer rows.Close()

	runNos := make(map[int64]int64, len(lineIDs))
	for rows.Next() {
		var lineID, runNo int64
		if err := rows.Scan(&lineID, &runNo); err != nil {
			return nil, fmt.Errorf("read a run number: %w", err)
		}
		runNos[lineID] = runNo
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the run numbers behind %d lines: %w", len(lineIDs), err)
	}
	return runNos, nil
}

// scanner is what *sql.Row and *sql.Rows have in common.
type scanner interface{ Scan(dest ...any) error }

func scanRun(row scanner) (Run, error) {
	var r Run
	var method, basis, status, createdAt, postedAt, voidedAt string
	err := row.Scan(&r.ID, &r.RunNo, &r.From, &r.To, &method, &r.Rule.RateBps, &basis, &r.Notes, &status,
		&createdAt, &r.CreatedBy.UserID, &r.CreatedBy.Username,
		&postedAt, &r.PostedBy.UserID, &r.PostedBy.Username,
		&voidedAt, &r.VoidedBy.UserID, &r.VoidedBy.Username, &r.VoidReason)
	if err != nil {
		return Run{}, err
	}

	r.Rule.Method, r.Rule.Basis, r.Status = Method(method), Basis(basis), Status(status)
	if r.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return Run{}, fmt.Errorf("interest run %d has an unreadable created_at %q: %w", r.ID, createdAt, err)
	}
	if r.PostedAt, err = optionalTime(postedAt); err != nil {
		return Run{}, fmt.Errorf("interest run %d has an unreadable posted_at %q: %w", r.ID, postedAt, err)
	}
	if r.VoidedAt, err = optionalTime(voidedAt); err != nil {
		return Run{}, fmt.Errorf("interest run %d has an unreadable voided_at %q: %w", r.ID, voidedAt, err)
	}
	return r, nil
}

// optionalTime reads a stamp that is absent until something has happened as nil
// rather than as the zero time, so a screen showing "posted at" has nothing to
// print rather than the year 1.
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
