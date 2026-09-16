package ledger

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// The two bulk reads step 7's interest run is computed from.
//
// Both are sums over entries, dates and entry types — the ledger's own
// vocabulary — so neither teaches this package anything about the documents
// above it. What is being summed is a TypeAdvance entry, not a voucher.
//
// They exist because the single-customer reads do not scale to a run. A preview
// over a station with 364 customers means 364 calls to BalanceAt, each a full
// scan of that customer's entries, to find the dozen farmers who actually owe
// something. One grouped query answers the same question once.
//
// That makes them a second way to compute a number the ledger can already
// compute, which is the shape that goes wrong quietly. The property tests
// therefore assert the thing that must be true rather than the thing that is
// convenient: over generated sequences, BalancesAt and BalanceAt cannot
// disagree about any customer, on any date.

// CustomerAmount is one customer and one figure about them.
type CustomerAmount struct {
	CustomerID int64
	Amount     money.Money
}

// BalancesAt returns what every customer owed at the end of a given day, summed
// from the entries rather than read from the cache.
//
// Customers whose balance comes to nothing at all are left out — not as an
// optimisation, but because they are the answer to a different question. A run
// charges who owes something; a list padded with three hundred zeroes hides the
// twelve rows that matter.
//
// A customer in credit *is* included, with a negative balance. Whether that is
// chargeable is the caller's rule and not the ledger's.
func (s *Service) BalancesAt(ctx context.Context, on Date) ([]Balance, error) {
	return balancesAt(ctx, s.db, on)
}

// BalancesAtTx is BalancesAt inside a transaction the caller already owns.
//
// Posting an interest run needs this: the figures it writes must be recomputed
// from the ledger as it stands inside the posting transaction, not as it stood
// on another connection before somebody's payment landed.
func (s *Service) BalancesAtTx(ctx context.Context, tx *sql.Tx, on Date) ([]Balance, error) {
	return balancesAt(ctx, tx, on)
}

func balancesAt(ctx context.Context, q querier, on Date) ([]Balance, error) {
	if on.IsZero() {
		return nil, fmt.Errorf("%w: BalancesAt needs a date", ErrInvalidEntry)
	}

	rows, err := q.QueryContext(ctx, `
		SELECT customer_id,
		       SUM(principal_delta_cents),
		       SUM(interest_delta_cents),
		       MAX(entry_date)
		  FROM ledger_entries
		 WHERE entry_date <= ?
		 GROUP BY customer_id
		HAVING SUM(principal_delta_cents) <> 0 OR SUM(interest_delta_cents) <> 0
		 ORDER BY customer_id`, on)
	if err != nil {
		return nil, fmt.Errorf("sum the ledger up to %s: %w", on, err)
	}
	defer rows.Close()

	var balances []Balance
	for rows.Next() {
		var b Balance
		if err := rows.Scan(&b.CustomerID, &b.Principal, &b.Interest, &b.LastActivity); err != nil {
			return nil, fmt.Errorf("read a balance as at %s: %w", on, err)
		}
		balances = append(balances, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sum the ledger up to %s: %w", on, err)
	}
	return balances, nil
}

// AdvancedBetween returns the principal each customer was advanced over a
// period, net of the reversals that undid it.
//
// **Netting is by what a reversal undoes, never by when it happened.** A
// reversal is dated the day of the void rather than the day of the entry it
// reverses — that is step 3's rule, and it is what keeps an old statement
// reproducible. It also means a voucher posted in September and voided in
// October leaves a September entry and an October reversal, and summing "every
// entry dated in September" would charge interest on money that has been taken
// back.
//
// So the reversal is joined to its target and subtracted wherever it sits. The
// join cannot multiply rows: the ledger's unique index allows an entry at most
// one reversal.
func (s *Service) AdvancedBetween(ctx context.Context, from, to Date) ([]CustomerAmount, error) {
	return advancedBetween(ctx, s.db, from, to)
}

// AdvancedBetweenTx is AdvancedBetween inside a transaction the caller owns.
func (s *Service) AdvancedBetweenTx(ctx context.Context, tx *sql.Tx, from, to Date) ([]CustomerAmount, error) {
	return advancedBetween(ctx, tx, from, to)
}

func advancedBetween(ctx context.Context, q querier, from, to Date) ([]CustomerAmount, error) {
	switch {
	case from.IsZero() || to.IsZero():
		return nil, fmt.Errorf("%w: AdvancedBetween needs both ends of a period", ErrInvalidEntry)
	case from.After(to):
		return nil, fmt.Errorf("%w: a period cannot run from %s back to %s", ErrInvalidEntry, from, to)
	}

	rows, err := q.QueryContext(ctx, `
		SELECT e.customer_id,
		       SUM(e.principal_delta_cents + COALESCE(r.principal_delta_cents, 0))
		  FROM ledger_entries e
		  LEFT JOIN ledger_entries r ON r.reverses_id = e.id
		 WHERE e.entry_type = ?
		   AND e.entry_date >= ? AND e.entry_date <= ?
		 GROUP BY e.customer_id
		HAVING SUM(e.principal_delta_cents + COALESCE(r.principal_delta_cents, 0)) <> 0
		 ORDER BY e.customer_id`, TypeAdvance, from, to)
	if err != nil {
		return nil, fmt.Errorf("sum what was advanced between %s and %s: %w", from, to, err)
	}
	defer rows.Close()

	var advanced []CustomerAmount
	for rows.Next() {
		var a CustomerAmount
		if err := rows.Scan(&a.CustomerID, &a.Amount); err != nil {
			return nil, fmt.Errorf("read what was advanced between %s and %s: %w", from, to, err)
		}
		advanced = append(advanced, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sum what was advanced between %s and %s: %w", from, to, err)
	}
	return advanced, nil
}
