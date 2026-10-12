package interest

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// Pagination bounds, as everywhere else: the ceiling is not about this
// station's volume, it is so a mistyped URL cannot ask for the whole table
// repeatedly on a machine that is also serving the counter.
const (
	DefaultPerPage = 50
	MaxPerPage     = 200
)

// Query is a search over the runs.
//
// There is no customer filter, and that absence is the point: a run is not
// about one farmer. "What interest has this farmer been charged?" is a question
// about their statement, which is where it is asked and answered.
type Query struct {
	// Text matches a run number. Empty matches everything.
	Text string

	// Status filters by where the run is in its life. Empty means all of them —
	// an unposted draft is something somebody needs to see rather than
	// something to go looking for.
	Status Status

	// From and To bound the period a run covers, by where it ends.
	From, To ledger.Date

	Page    int // 1-based
	PerPage int
}

// Summary is one run as the list shows it: the period, the rule, and the size
// of what it did.
type Summary struct {
	ID    int64 `json:"id"`
	RunNo int64 `json:"run_no"`

	From ledger.Date `json:"period_from"`
	To   ledger.Date `json:"period_to"`

	Rule   Rule   `json:"rule"`
	Status Status `json:"status"`

	// Customers and Total are the two numbers a manager reads the list for:
	// how many farmers a run touched, and how much it charged them. Both are
	// computed in SQL rather than by loading every line.
	Customers int         `json:"customers"`
	Total     money.Money `json:"total"`

	CreatedBy string `json:"created_by"`
}

// Page is one page of results plus what a pager needs to draw itself.
type Page struct {
	Runs    []Summary `json:"runs"`
	Total   int       `json:"total"`
	Page    int       `json:"page"`
	PerPage int       `json:"per_page"`
}

func (q Query) normalise() Query {
	if !q.Status.Valid() {
		q.Status = ""
	}
	if q.Page < 1 {
		q.Page = 1
	}
	switch {
	case q.PerPage < 1:
		q.PerPage = DefaultPerPage
	case q.PerPage > MaxPerPage:
		q.PerPage = MaxPerPage
	}
	q.Text = strings.TrimSpace(q.Text)
	return q
}

// List searches the runs, most recent period first.
func (s *Service) List(ctx context.Context, q Query) (Page, error) {
	q = q.normalise()
	where, args := q.condition()

	page := Page{Runs: []Summary{}, Page: q.Page, PerPage: q.PerPage}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM interest_runs r `+where, args...).Scan(&page.Total); err != nil {
		return Page{}, fmt.Errorf("count interest runs: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, COALESCE(r.run_no, 0), r.period_from, r.period_to,
		        r.method, r.rate_bps, r.basis, r.status, r.created_by_username,
		        (SELECT COUNT(*)                       FROM interest_run_lines l WHERE l.run_id = r.id),
		        (SELECT COALESCE(SUM(charge_cents), 0) FROM interest_run_lines l WHERE l.run_id = r.id)
		   FROM interest_runs r
		   `+where+`
		  ORDER BY r.period_to DESC, r.id DESC
		  LIMIT ? OFFSET ?`,
		append(args, q.PerPage, (q.Page-1)*q.PerPage)...)
	if err != nil {
		return Page{}, fmt.Errorf("list interest runs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sum Summary
		var method, basis, status string
		if err := rows.Scan(&sum.ID, &sum.RunNo, &sum.From, &sum.To,
			&method, &sum.Rule.RateBps, &basis, &status, &sum.CreatedBy,
			&sum.Customers, &sum.Total); err != nil {
			return Page{}, fmt.Errorf("read an interest run: %w", err)
		}
		sum.Rule.Method, sum.Rule.Basis, sum.Status = Method(method), Basis(basis), Status(status)
		page.Runs = append(page.Runs, sum)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("list interest runs: %w", err)
	}
	return page, nil
}

// condition builds the WHERE clause and its arguments.
func (q Query) condition() (string, []any) {
	var clauses []string
	var args []any

	if q.Text != "" {
		// A run number is the only text on this screen. A search that is not a
		// number matches nothing rather than everything: the alternative is a
		// typo silently returning the whole table.
		if runNo, err := strconv.ParseInt(q.Text, 10, 64); err == nil {
			clauses = append(clauses, `r.run_no = ?`)
			args = append(args, runNo)
		} else {
			clauses = append(clauses, `1 = 0`)
		}
	}
	if q.Status != "" {
		clauses = append(clauses, `r.status = ?`)
		args = append(args, string(q.Status))
	}
	if !q.From.IsZero() {
		clauses = append(clauses, `r.period_to >= ?`)
		args = append(args, q.From)
	}
	if !q.To.IsZero() {
		clauses = append(clauses, `r.period_to <= ?`)
		args = append(args, q.To)
	}

	if len(clauses) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}
