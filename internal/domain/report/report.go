// Package report is the four pages somebody prints and carries out of the room.
//
// Everything before this package answers a question about one farmer, at a
// counter, while they are standing there. These answer questions about the whole
// book: who owes money, whether the lending is working, how old the debt is, and
// who has finished paying.
//
// **It reads and never writes.** There is no transaction anywhere in here, no
// INSERT, and no method that takes one — which is the answer to Q4 of the domain
// interview expressed as code rather than as a promise. "Recall Liquidated
// Transaction" might turn out to mean reopening a settled account; if it does,
// that is a deliberate correction workflow that posts entries, not a door left
// ajar in a report.
//
// It sits above both the ledger and the documents, and may ask each what it
// knows: the ledger for what money did, `customer` for who somebody is,
// `advance` for how many vouchers a period held. That is the same boundary the
// statement's label resolver respects, one level up — the ledger still knows
// nothing about vouchers.
package report

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// Service builds the reports.
//
// It holds the ledger rather than a database handle for the arithmetic, and a
// handle only for the two things the ledger cannot answer: who a customer is,
// and how many vouchers a period held.
type Service struct {
	db     *sql.DB
	ledger *ledger.Service
	log    *slog.Logger
}

func New(database *sql.DB, entries *ledger.Service, log *slog.Logger) *Service {
	return &Service{db: database, ledger: entries, log: log}
}

// Who is a customer as every report names them: the code staff say out loud,
// and the name on the slip.
type Who struct {
	CustomerID int64  `json:"customer_id"`
	Code       string `json:"code"`
	Name       string `json:"name"`
}

// names reads the code and name of everybody the caller is about to print.
//
// One query for the page rather than one per row: a report over 364 customers
// that looked each one up separately would be 364 round trips to answer a
// question the database can answer once.
func (s *Service) names(ctx context.Context, ids []int64) (map[int64]Who, error) {
	if len(ids) == 0 {
		return map[int64]Who{}, nil
	}

	marks := make([]byte, 0, len(ids)*3)
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		if i > 0 {
			marks = append(marks, ',', ' ')
		}
		marks = append(marks, '?')
		args = append(args, id)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, code, name FROM customers WHERE id IN (`+string(marks)+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("read the names on a report: %w", err)
	}
	defer rows.Close()

	who := make(map[int64]Who, len(ids))
	for rows.Next() {
		var w Who
		if err := rows.Scan(&w.CustomerID, &w.Code, &w.Name); err != nil {
			return nil, fmt.Errorf("read a name on a report: %w", err)
		}
		who[w.CustomerID] = w
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the names on a report: %w", err)
	}
	return who, nil
}

// byCustomer groups deltas by who they belong to, keeping each customer's in
// the date order the query returned them in.
func byCustomer(deltas []ledger.Delta) map[int64][]ledger.Delta {
	grouped := make(map[int64][]ledger.Delta)
	for _, d := range deltas {
		grouped[d.CustomerID] = append(grouped[d.CustomerID], d)
	}
	return grouped
}

// sumMoney adds a column up in Go, over money.Money, so an overflow is an error
// rather than a figure that looks fine.
func sumMoney(amounts ...money.Money) (money.Money, error) {
	return money.Sum(amounts...)
}

// share is one amount as a percentage of another, to one decimal place, as text.
//
// Integer arithmetic throughout and no float anywhere near it: the numerator is
// scaled by a thousand before dividing, which gives tenths of a percent exactly,
// and the text is assembled from the digits. A percentage on a printed report is
// read by somebody deciding whether the lending is working, and "70.2%" arrived
// at by float division is a number nobody can reproduce by hand.
//
// Nothing out of nothing is an empty string rather than a zero: a station that
// lent nothing this period has no recovery rate, and printing "0.0%" would say
// it recovered none of it.
func share(part, whole money.Money) string {
	if !whole.IsPositive() {
		return ""
	}

	tenths := part.Centavos() * 1000 / whole.Centavos()
	if tenths < 0 {
		tenths = 0
	}
	return fmt.Sprintf("%d.%d%%", tenths/10, tenths%10)
}
