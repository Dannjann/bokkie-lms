package customer

import (
	"context"
	"fmt"
	"strings"
)

// Status is which customers a listing includes.
type Status string

const (
	// StatusActive is the default. The everyday question at a counter is about
	// somebody currently on the books, and a deactivated duplicate showing up
	// in the results is exactly the confusion deactivation was meant to end.
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
	StatusAny      Status = "all"
)

// Valid reports whether s is a status this package knows.
func (s Status) Valid() bool {
	return s == StatusActive || s == StatusInactive || s == StatusAny
}

// Pagination bounds. The per-page ceiling is not about this station's 364
// customers; it is so that a mistyped URL cannot ask for the whole table
// repeatedly on a machine that is also serving the counter.
const (
	DefaultPerPage = 50
	MaxPerPage     = 200
)

// Query is a customer search.
type Query struct {
	// Text matches against code, name and cell number. Empty matches everyone.
	Text string

	Status  Status
	Page    int // 1-based
	PerPage int
}

// Page is one page of results plus what a pager needs to draw itself.
type Page struct {
	Customers []Customer `json:"customers"`
	Total     int        `json:"total"`
	Page      int        `json:"page"`
	PerPage   int        `json:"per_page"`
}

// normalise fills in defaults and clamps. A caller passing nothing gets the
// first page of active customers, which is what a freshly opened screen wants.
func (q Query) normalise() Query {
	if !q.Status.Valid() {
		q.Status = StatusActive
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

// List searches the master.
//
// This is the query Step 5's voucher-line picker and Step 8's reports will both
// reuse, which is why it is one method with a filter rather than a method per
// screen.
func (s *Service) List(ctx context.Context, q Query) (Page, error) {
	q = q.normalise()

	where, args := q.condition()

	page := Page{Customers: []Customer{}, Page: q.Page, PerPage: q.PerPage}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM customers `+where, args...).Scan(&page.Total); err != nil {
		return Page{}, fmt.Errorf("count customers: %w", err)
	}

	// ORDER BY code is ORDER BY the sequence, because the format is
	// zero-padded to a fixed width. That is the whole reason it is.
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+selectColumns+` FROM customers `+where+` ORDER BY code LIMIT ? OFFSET ?`,
		append(args, q.PerPage, (q.Page-1)*q.PerPage)...)
	if err != nil {
		return Page{}, fmt.Errorf("list customers: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return Page{}, fmt.Errorf("list customers: %w", err)
		}
		page.Customers = append(page.Customers, c)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("list customers: %w", err)
	}
	return page, nil
}

// condition builds the WHERE shared by the count and the page, so the two can
// never disagree about what is being counted.
func (q Query) condition() (string, []any) {
	var (
		clauses []string
		args    []any
	)

	switch q.Status {
	case StatusActive:
		clauses = append(clauses, "active = 1")
	case StatusInactive:
		clauses = append(clauses, "active = 0")
	}

	if q.Text != "" {
		// The stored cell number is digits only, so a search for "0917-123"
		// has to be reduced the same way the number was before it can match.
		// When the text has no digits in it that reduction is empty, which
		// would make the pattern match everyone — so fall back to the plain
		// text, which simply will not match a numeric column.
		text := "%" + escapeLike(q.Text) + "%"
		cell := text
		if digits := NormaliseCell(q.Text); digits != "" {
			cell = "%" + escapeLike(digits) + "%"
		}

		clauses = append(clauses,
			`(code LIKE ? ESCAPE '\' OR name LIKE ? ESCAPE '\' OR cell_no LIKE ? ESCAPE '\')`)
		args = append(args, text, text, cell)
	}

	if len(clauses) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

// escapeLike neutralises LIKE's metacharacters.
//
// A farmer's name containing an underscore is unlikely. Somebody typing % into
// a search box to see what happens is not, and without this it would silently
// return the entire master and look like a broken filter.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLike(s string) string { return likeEscaper.Replace(s) }
