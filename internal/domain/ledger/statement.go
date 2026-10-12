package ledger

import (
	"context"
	"fmt"

	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// A statement is what a farmer is shown when they ask what they owe and why:
// an opening balance, every entry between two dates, and the balance after
// each one.
//
// It is a read over this package and nothing else. It does not know that
// vouchers or receipts exist — a row carries the source table and id of the
// document behind it, and turning that into "Ref. No. 1425" is done above the
// ledger by something that is allowed to know what those documents are.

// StatementRow is one entry and the balance it left behind.
type StatementRow struct {
	Entry Entry `json:"entry"`

	// Running is the balance after this entry, counting everything before it —
	// including entries older than the statement's own window.
	Running Balance `json:"running"`
}

// Statement is one customer's history over a period.
type Statement struct {
	CustomerID int64 `json:"customer_id"`

	From Date `json:"from"`
	To   Date `json:"to"`

	// Opening is what was owed at the end of the day before From. Zero when the
	// statement runs from the beginning.
	Opening Balance `json:"opening"`

	Rows []StatementRow `json:"rows"`

	// Closing is what was owed at the end of To. It must equal BalanceAt(To),
	// and that equality is this type's one real invariant — a statement whose
	// last line disagrees with the balance the rest of the system shows is
	// worse than no statement at all.
	Closing Balance `json:"closing"`
}

// Statement returns one customer's entries between two dates, with a running
// balance on every row.
//
// A zero from means "since the beginning"; a zero to means "up to today". The
// running balance accumulates in Go, over money.Money, so a total that cannot
// be held is an error rather than a number that looks fine — and so that no
// peso figure in this system is ever added up in a browser.
//
// Reversals appear like any other entry. A statement that hides corrections is
// a statement that lies, and the farmer holding the paper slip is the person
// most entitled to see that something was undone.
func (s *Service) Statement(ctx context.Context, customerID int64, from, to Date) (Statement, error) {
	if to.IsZero() {
		to = s.today()
	}
	if !from.IsZero() && from.After(to) {
		return Statement{}, fmt.Errorf("%w: a statement cannot run from %s back to %s", ErrInvalidEntry, from, to)
	}

	stmt := Statement{CustomerID: customerID, From: from, To: to, Rows: []StatementRow{}}

	// The opening balance is everything before the window. Without it a
	// statement of the last month reads as though the farmer started it owing
	// nothing.
	if !from.IsZero() {
		opening, err := s.BalanceAt(ctx, customerID, from.AddDays(-1))
		if err != nil {
			return Statement{}, err
		}
		stmt.Opening = opening
	}
	stmt.Opening.CustomerID = customerID

	entries, err := s.StatementFor(ctx, customerID, from, to)
	if err != nil {
		return Statement{}, err
	}

	running := Balance{
		CustomerID: customerID,
		Principal:  stmt.Opening.Principal,
		Interest:   stmt.Opening.Interest,
	}
	for _, entry := range entries {
		if running.Principal, err = running.Principal.Add(entry.Principal); err != nil {
			return Statement{}, fmt.Errorf("run the principal forward to entry %d: %w", entry.ID, err)
		}
		if running.Interest, err = running.Interest.Add(entry.Interest); err != nil {
			return Statement{}, fmt.Errorf("run the interest forward to entry %d: %w", entry.ID, err)
		}
		running.LastActivity = entry.Date

		stmt.Rows = append(stmt.Rows, StatementRow{Entry: entry, Running: running})
	}

	stmt.Closing = running
	if len(stmt.Rows) == 0 {
		// A customer nothing happened to in the window still closes where they
		// opened, rather than at zero.
		stmt.Closing = stmt.Opening
	}
	return stmt, nil
}

// Movement is what the entries in a statement did, in total: how much was lent,
// charged, paid and reversed over the period.
//
// It exists because the question a statement is usually opened to answer is
// "how much has this farmer paid this season", and counting the rows by eye is
// how people get that wrong. Every figure is signed the way the ledger signs
// everything: positive increases what is owed.
type Movement struct {
	Principal money.Money `json:"principal"`
	Interest  money.Money `json:"interest"`
}

// Movement totals a statement's rows.
func (s Statement) Movement() (Movement, error) {
	var m Movement
	var err error
	for _, row := range s.Rows {
		if m.Principal, err = m.Principal.Add(row.Entry.Principal); err != nil {
			return Movement{}, fmt.Errorf("total the principal of a statement: %w", err)
		}
		if m.Interest, err = m.Interest.Add(row.Entry.Interest); err != nil {
			return Movement{}, fmt.Errorf("total the interest of a statement: %w", err)
		}
	}
	return m, nil
}
