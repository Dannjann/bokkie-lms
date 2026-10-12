package advance

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// Pagination bounds, as in the customer master: the ceiling is not about this
// station's volume, it is so a mistyped URL cannot ask for the whole table
// repeatedly on a machine that is also serving the counter.
const (
	DefaultPerPage = 50
	MaxPerPage     = 200
)

// Query is a voucher search.
type Query struct {
	// Text matches a Ref. No., or the code or name of a customer on a line.
	// Empty matches everything.
	Text string

	// Status filters by where the voucher is in its life. Empty means all of
	// them, which is the default the list screen wants: an unfinished draft is
	// something somebody needs to see, not something to go looking for.
	Status Status

	From, To ledger.Date

	Page    int // 1-based
	PerPage int
}

// Summary is one voucher as the list shows it: enough to recognise it by,
// without its lines.
type Summary struct {
	ID     int64       `json:"id"`
	RefNo  int64       `json:"ref_no"`
	Date   ledger.Date `json:"date"`
	Status Status      `json:"status"`

	// Total and LineCount come from the lines without carrying them. Adding up
	// a column of pesos is the server's job in every screen of this system.
	Total     money.Money `json:"total"`
	LineCount int         `json:"line_count"`

	// Customers is who is on the voucher: one name when a voucher is one
	// farmer's, which is the common case if Q6 of the domain interview comes
	// back the way the owner's grid suggests.
	Customers []string `json:"customers"`

	CreatedBy string `json:"created_by"`
}

// Page is one page of results plus what a pager needs to draw itself.
type Page struct {
	Vouchers []Summary `json:"vouchers"`
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

// List searches the vouchers.
//
// Newest first, by date and then by id — which for posted vouchers is also
// Ref. No. order, and which puts today's drafts at the top where whoever has to
// finish them will see them.
func (s *Service) List(ctx context.Context, q Query) (Page, error) {
	q = q.normalise()
	where, args := q.condition()

	page := Page{Vouchers: []Summary{}, Page: q.Page, PerPage: q.PerPage}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM advance_vouchers v `+where, args...).Scan(&page.Total); err != nil {
		return Page{}, fmt.Errorf("count vouchers: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT v.id, COALESCE(v.ref_no, 0), v.voucher_date, v.status, v.created_by_username,
		        COALESCE(SUM(l.amount_cents), 0), COUNT(l.id)
		   FROM advance_vouchers v
		   LEFT JOIN advance_lines l ON l.voucher_id = v.id
		   `+where+`
		  GROUP BY v.id
		  ORDER BY v.voucher_date DESC, v.id DESC
		  LIMIT ? OFFSET ?`,
		append(args, q.PerPage, (q.Page-1)*q.PerPage)...)
	if err != nil {
		return Page{}, fmt.Errorf("list vouchers: %w", err)
	}
	defer rows.Close()

	ids := make([]any, 0, q.PerPage)
	for rows.Next() {
		var sum Summary
		var status string
		if err := rows.Scan(&sum.ID, &sum.RefNo, &sum.Date, &status, &sum.CreatedBy,
			&sum.Total, &sum.LineCount); err != nil {
			return Page{}, fmt.Errorf("list vouchers: %w", err)
		}
		sum.Status = Status(status)
		sum.Customers = []string{}
		page.Vouchers = append(page.Vouchers, sum)
		ids = append(ids, sum.ID)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("list vouchers: %w", err)
	}

	if err := s.attachCustomers(ctx, page.Vouchers, ids); err != nil {
		return Page{}, err
	}
	return page, nil
}

// attachCustomers fills in who is on each voucher, in one query for the whole
// page rather than one per row.
func (s *Service) attachCustomers(ctx context.Context, vouchers []Summary, ids []any) error {
	if len(ids) == 0 {
		return nil
	}

	marks := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT l.voucher_id, c.name
		   FROM advance_lines l
		   JOIN customers c ON c.id = l.customer_id
		  WHERE l.voucher_id IN (`+marks+`)
		  ORDER BY l.voucher_id, c.name`, ids...)
	if err != nil {
		return fmt.Errorf("read the customers on a page of vouchers: %w", err)
	}
	defer rows.Close()

	names := make(map[int64][]string, len(ids))
	for rows.Next() {
		var voucherID int64
		var name string
		if err := rows.Scan(&voucherID, &name); err != nil {
			return fmt.Errorf("read the customers on a page of vouchers: %w", err)
		}
		names[voucherID] = append(names[voucherID], name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read the customers on a page of vouchers: %w", err)
	}

	for i := range vouchers {
		if found, ok := names[vouchers[i].ID]; ok {
			vouchers[i].Customers = found
		}
	}
	return nil
}

// condition builds the WHERE shared by the count and the page, so the two
// cannot disagree about what is being counted.
func (q Query) condition() (string, []any) {
	var (
		clauses []string
		args    []any
	)

	if q.Status != "" {
		clauses = append(clauses, "v.status = ?")
		args = append(args, string(q.Status))
	}
	if !q.From.IsZero() {
		clauses = append(clauses, "v.voucher_date >= ?")
		args = append(args, q.From)
	}
	if !q.To.IsZero() {
		clauses = append(clauses, "v.voucher_date <= ?")
		args = append(args, q.To)
	}

	if q.Text != "" {
		// A Ref. No. is typed as a number and matched exactly: "1425" finds
		// voucher 1425, not every voucher with 1425 somewhere in it. Anything
		// else is a substring match against the customers on the voucher, which
		// is how somebody looks for "that voucher for Juan".
		text := "%" + escapeLike(q.Text) + "%"
		onALine := `EXISTS (SELECT 1 FROM advance_lines l JOIN customers c ON c.id = l.customer_id
		                     WHERE l.voucher_id = v.id
		                       AND (c.code LIKE ? ESCAPE '\' OR c.name LIKE ? ESCAPE '\'))`

		// The arguments are appended in the order their placeholders appear,
		// which is why the Ref. No. goes on before the two patterns rather
		// than at the front of everything.
		if refNo, err := strconv.ParseInt(q.Text, 10, 64); err == nil {
			clauses = append(clauses, "(v.ref_no = ? OR "+onALine+")")
			args = append(args, refNo, text, text)
		} else {
			clauses = append(clauses, onALine)
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

// RefNosForLines maps line ids to the Ref. No. of the voucher they belong to.
//
// It exists for the customer statement, which holds ledger entries carrying the
// id of the line that produced them and needs to print the number staff say out
// loud. The ledger cannot answer that — it is not allowed to know vouchers
// exist — so the question is asked here, of the package that owns them, in one
// query for a whole page rather than one per row.
//
// A line whose voucher is still a draft has no number and is simply absent from
// the result: a draft has produced no ledger entry either, so nothing can ask
// about it.
func (s *Service) RefNosForLines(ctx context.Context, lineIDs []int64) (map[int64]int64, error) {
	if len(lineIDs) == 0 {
		return nil, nil
	}

	args := make([]any, 0, len(lineIDs))
	for _, id := range lineIDs {
		args = append(args, id)
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(lineIDs)), ", ")

	rows, err := s.db.QueryContext(ctx,
		`SELECT l.id, v.ref_no
		   FROM advance_lines l
		   JOIN advance_vouchers v ON v.id = l.voucher_id
		  WHERE l.id IN (`+marks+`) AND v.ref_no IS NOT NULL`, args...)
	if err != nil {
		return nil, fmt.Errorf("read the Ref. Nos. behind a page of entries: %w", err)
	}
	defer rows.Close()

	refNos := make(map[int64]int64, len(lineIDs))
	for rows.Next() {
		var lineID, refNo int64
		if err := rows.Scan(&lineID, &refNo); err != nil {
			return nil, fmt.Errorf("read the Ref. Nos. behind a page of entries: %w", err)
		}
		refNos[lineID] = refNo
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the Ref. Nos. behind a page of entries: %w", err)
	}
	return refNos, nil
}
