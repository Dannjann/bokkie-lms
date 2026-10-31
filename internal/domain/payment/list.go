package payment

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// Pagination bounds, as in the customer master and the voucher list: the
// ceiling is not about this station's volume, it is so a mistyped URL cannot
// ask for the whole table repeatedly on a machine that is also serving the
// counter.
const (
	DefaultPerPage = 50
	MaxPerPage     = 200
)

// Query is a payment search.
type Query struct {
	// Text matches an OR No., or the code or name of the customer who paid.
	// Empty matches everything.
	Text string

	// CustomerID narrows to one farmer. Zero means all of them. This is what
	// the customer screen's "payments" list is, and what a statement's
	// companion view uses.
	CustomerID int64

	// Status filters by where the payment is in its life. Empty means all of
	// them, which is what the list screen wants: an unfinished draft is
	// something somebody needs to see, not something to go looking for.
	Status Status

	From, To ledger.Date

	Page    int // 1-based
	PerPage int
}

// Summary is one payment as the list shows it.
//
// It carries no split between principal and interest. That lives on the ledger
// entry, and putting it on a list row would mean a join per page for a figure
// nobody reads at a glance — what a list is scanned for is who paid, how much,
// and whether it is posted.
type Summary struct {
	ID   int64 `json:"id"`
	OrNo int64 `json:"or_no"`

	CustomerID   int64  `json:"customer_id"`
	CustomerCode string `json:"customer_code"`
	CustomerName string `json:"customer_name"`

	Date       ledger.Date `json:"date"`
	Amount     money.Money `json:"amount"`
	Method     Method      `json:"method"`
	Particular string      `json:"particular"`
	Status     Status      `json:"status"`

	// SettlementNo is the posted settlement that applied this payment, or
	// zero while it is still waiting to be settled.
	SettlementID int64 `json:"settlement_id"`
	SettlementNo int64 `json:"settlement_no"`

	CreatedBy string `json:"created_by"`
}

// Page is one page of results plus what a pager needs to draw itself.
type Page struct {
	Payments []Summary `json:"payments"`
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	PerPage  int       `json:"per_page"`
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

// List searches the payments.
//
// Newest first, by date and then by id — which for posted payments is also OR
// No. order, and which puts today's drafts at the top where whoever has to
// finish them will see them.
func (s *Service) List(ctx context.Context, q Query) (Page, error) {
	q = q.normalise()
	where, args := q.condition()

	page := Page{Payments: []Summary{}, Page: q.Page, PerPage: q.PerPage}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM payments p JOIN customers c ON c.id = p.customer_id `+where,
		args...).Scan(&page.Total); err != nil {
		return Page{}, fmt.Errorf("count payments: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT p.id, COALESCE(p.or_no, 0), p.customer_id, c.code, c.name,
		        p.payment_date, p.amount_cents, p.method, p.particular, p.status,
		        p.created_by_username,
		        COALESCE(st.id, 0), COALESCE(st.settlement_no, 0)
		   FROM payments p
		   JOIN customers c ON c.id = p.customer_id
		   LEFT JOIN settlement_payments sp ON sp.payment_id = p.id
		         AND sp.settlement_id IN (SELECT id FROM settlements WHERE status = 'posted')
		   LEFT JOIN settlements st ON st.id = sp.settlement_id
		   `+where+`
		  ORDER BY p.payment_date DESC, p.id DESC
		  LIMIT ? OFFSET ?`,
		append(args, q.PerPage, (q.Page-1)*q.PerPage)...)
	if err != nil {
		return Page{}, fmt.Errorf("list payments: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sum Summary
		var method, status string
		if err := rows.Scan(&sum.ID, &sum.OrNo, &sum.CustomerID, &sum.CustomerCode, &sum.CustomerName,
			&sum.Date, &sum.Amount, &method, &sum.Particular, &status, &sum.CreatedBy,
			&sum.SettlementID, &sum.SettlementNo); err != nil {
			return Page{}, fmt.Errorf("list payments: %w", err)
		}
		sum.Method, sum.Status = Method(method), Status(status)
		page.Payments = append(page.Payments, sum)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("list payments: %w", err)
	}
	return page, nil
}

// condition builds the WHERE shared by the count and the page, so the two
// cannot disagree about what is being counted.
func (q Query) condition() (string, []any) {
	var (
		clauses []string
		args    []any
	)

	if q.CustomerID > 0 {
		clauses = append(clauses, "p.customer_id = ?")
		args = append(args, q.CustomerID)
	}
	if q.Status != "" {
		clauses = append(clauses, "p.status = ?")
		args = append(args, string(q.Status))
	}
	if !q.From.IsZero() {
		clauses = append(clauses, "p.payment_date >= ?")
		args = append(args, q.From)
	}
	if !q.To.IsZero() {
		clauses = append(clauses, "p.payment_date <= ?")
		args = append(args, q.To)
	}

	if q.Text != "" {
		// An OR No. is typed as a number and matched exactly: "7" finds receipt
		// 7, not every receipt with a 7 somewhere in it. Anything else is a
		// substring match against the customer, which is how somebody looks for
		// "that payment from Juan".
		text := "%" + escapeLike(q.Text) + "%"
		theCustomer := `(c.code LIKE ? ESCAPE '\' OR c.name LIKE ? ESCAPE '\')`

		// The arguments are appended in the order their placeholders appear,
		// which is why the OR No. goes on before the two patterns rather than
		// at the front of everything.
		if orNo, err := strconv.ParseInt(q.Text, 10, 64); err == nil {
			clauses = append(clauses, "(p.or_no = ? OR "+theCustomer+")")
			args = append(args, orNo, text, text)
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

// escapeLike neutralises LIKE's metacharacters, for the same reason the
// customer master does: somebody typing % into a search box to see what happens
// would otherwise get the entire table back and think the filter is broken.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLike(s string) string { return likeEscaper.Replace(s) }

// Document is what a statement shows of the payment behind an entry.
type Document struct {
	OrNo       int64
	Particular string
}

// DocumentsFor maps payment ids to their OR Nos. and particulars, for the
// statement — the same question DocumentsForLines answers for vouchers, and asked here for the same
// reason: the ledger holds the provenance and is not allowed to know what a
// receipt is.
//
// A draft has no number and is absent from the result, which is consistent with
// it having produced no entry to ask about.
func (s *Service) DocumentsFor(ctx context.Context, ids []int64) (map[int64]Document, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, or_no, particular FROM payments WHERE id IN (`+marks+`) AND or_no IS NOT NULL`, args...)
	if err != nil {
		return nil, fmt.Errorf("read the OR Nos. behind a page of entries: %w", err)
	}
	defer rows.Close()

	docs := make(map[int64]Document, len(ids))
	for rows.Next() {
		var id int64
		var doc Document
		if err := rows.Scan(&id, &doc.OrNo, &doc.Particular); err != nil {
			return nil, fmt.Errorf("read the OR Nos. behind a page of entries: %w", err)
		}
		docs[id] = doc
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the OR Nos. behind a page of entries: %w", err)
	}
	return docs, nil
}
