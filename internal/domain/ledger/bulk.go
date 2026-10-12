package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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

// Activity is what one customer did over a period, split the four ways step 8's
// ROI report asks about.
//
// Named apart from Movement, which is the statement's signed pair of deltas.
// These are quantities of money that moved rather than the direction a balance
// went, and conflating the two is how a report ends up subtracting something it
// meant to add.
//
// Advanced and Repaid are principal; InterestCharged and InterestCollected are
// interest. All four are positive figures — they are quantities of money that
// moved, not deltas — which is what lets a report add them up without deciding
// per column which way the sign runs.
type Activity struct {
	CustomerID int64

	Advanced          money.Money // principal lent
	Repaid            money.Money // principal that came back
	InterestCharged   money.Money // interest posted against them
	InterestCollected money.Money // interest cleared by their payments
}

// ActivityBetween returns what each customer did over a period, net of the
// reversals that undid it.
//
// A voucher posted in September and voided in October leaves a September entry
// and an October reversal, so summing entries by date alone would count money
// that came back. Each entry is joined to its reversal and the two added
// together, which makes a voided document contribute exactly nothing.
//
// **But only a reversal that had happened by the end of the period counts.**
// This is the ledger's own rule and it is worth being exact about, because the
// obvious implementation gets it wrong: Reverse dates a reversal the day of the
// void precisely so that "a statement printed in February still says in March
// what it said in February", and a report that netted a March void out of
// February's figures would break that in the other direction. So the join
// carries the same upper bound as the entries do, and a report over a period
// reads as the books read at the end of it — which is also what keeps Deployed
// and Outstanding on the same page telling the same story.
//
// AdvancedBetween deliberately does *not* bound its join, and the difference is
// not an inconsistency: it feeds an interest charge rather than a report, and
// charging a farmer interest on money that has since been handed back is
// indefensible whenever the handing back happened.
//
// The join cannot multiply rows: the ledger's unique index allows an entry at
// most one reversal.
func (s *Service) ActivityBetween(ctx context.Context, from, to Date) ([]Activity, error) {
	return activityBetween(ctx, s.db, from, to)
}

// ActivityBetweenTx is ActivityBetween inside a transaction the caller owns.
func (s *Service) ActivityBetweenTx(ctx context.Context, tx *sql.Tx, from, to Date) ([]Activity, error) {
	return activityBetween(ctx, tx, from, to)
}

func activityBetween(ctx context.Context, q querier, from, to Date) ([]Activity, error) {
	switch {
	case to.IsZero():
		return nil, fmt.Errorf("%w: ActivityBetween needs an end date", ErrInvalidEntry)
	case !from.IsZero() && from.After(to):
		return nil, fmt.Errorf("%w: a period cannot run from %s back to %s", ErrInvalidEntry, from, to)
	}

	// A zero from means "since the beginning", which is what the first report
	// of a season wants and what aging needs when it asks how much has been
	// repaid in total.
	start := from
	if start.IsZero() {
		start = MustDate(1, time.January, 1)
	}

	// Each figure is a sum over one side of one column, with the sign taken off
	// at the end: a payment's principal delta is negative, and "repaid" is a
	// quantity. The CASE arms are on the *entry's* type, so a reversal is
	// attributed to whatever it undoes rather than to a category of its own.
	rows, err := q.QueryContext(ctx, `
		SELECT e.customer_id,
		       COALESCE(SUM(CASE WHEN e.entry_type = 'advance'
		                         THEN e.principal_delta_cents + COALESCE(r.principal_delta_cents, 0)
		                         ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN e.entry_type = 'payment'
		                         THEN -(e.principal_delta_cents + COALESCE(r.principal_delta_cents, 0))
		                         ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN e.entry_type = 'interest'
		                         THEN e.interest_delta_cents + COALESCE(r.interest_delta_cents, 0)
		                         ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN e.entry_type = 'payment'
		                         THEN -(e.interest_delta_cents + COALESCE(r.interest_delta_cents, 0))
		                         ELSE 0 END), 0)
		  FROM ledger_entries e
		  LEFT JOIN ledger_entries r ON r.reverses_id = e.id AND r.entry_date <= ?
		 WHERE e.entry_date >= ? AND e.entry_date <= ?
		   AND e.entry_type <> 'reversal'
		 GROUP BY e.customer_id
		 ORDER BY e.customer_id`, to, start, to)
	if err != nil {
		return nil, fmt.Errorf("sum what moved between %s and %s: %w", from, to, err)
	}
	defer rows.Close()

	activity := []Activity{}
	for rows.Next() {
		var m Activity
		if err := rows.Scan(&m.CustomerID, &m.Advanced, &m.Repaid,
			&m.InterestCharged, &m.InterestCollected); err != nil {
			return nil, fmt.Errorf("read what moved between %s and %s: %w", from, to, err)
		}

		// A customer whose every document in the period was voided sums to four
		// zeroes. They did nothing, so they are not on the report.
		if m.Advanced.IsZero() && m.Repaid.IsZero() &&
			m.InterestCharged.IsZero() && m.InterestCollected.IsZero() {
			continue
		}
		activity = append(activity, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sum what moved between %s and %s: %w", from, to, err)
	}
	return activity, nil
}

// Delta is what one entry did to one customer's balance, on a day.
//
// Both columns, because two reports need different halves of the same walk:
// aging cares about principal alone — interest is charged against a balance
// rather than lent on a day, so it has no age — while working out when somebody
// finished paying needs the total. One read, walked twice.
type Delta struct {
	CustomerID int64
	Date       Date

	Principal money.Money
	Interest  money.Money
}

// Total is the movement in the balance, which is what "did this take them to
// zero" is asked of.
func (d Delta) Total() (money.Money, error) { return d.Principal.Add(d.Interest) }

// DeltasUpTo returns every entry up to a date as the movement it made, oldest
// first, net of the reversals that undid it.
//
// This is what aging is computed from, and it deliberately does not ask what
// kind of entry each one was. **Any positive principal movement is money that
// went out on a day; any negative one is money that came back.** An opening
// balance at cutover is outstanding principal that is already old; an
// adjustment can move principal either way. Enumerating entry types here would
// mean a type added in some later step falls silently outside the report —
// summing the column cannot.
//
// Both signs come back in one list, in date order, which is exactly the shape a
// FIFO walk consumes.
//
// A reversal counts only if it had happened by the date asked about, for the
// reason ActivityBetween gives: a report as at a day reads as the books read on
// that day, and the ledger dates a reversal deliberately so that stays true.
func (s *Service) DeltasUpTo(ctx context.Context, on Date) ([]Delta, error) {
	if on.IsZero() {
		return nil, fmt.Errorf("%w: DeltasUpTo needs a date", ErrInvalidEntry)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT e.customer_id, e.entry_date,
		       e.principal_delta_cents + COALESCE(r.principal_delta_cents, 0),
		       e.interest_delta_cents  + COALESCE(r.interest_delta_cents, 0)
		  FROM ledger_entries e
		  LEFT JOIN ledger_entries r ON r.reverses_id = e.id AND r.entry_date <= ?
		 WHERE e.entry_date <= ?
		   AND e.entry_type <> 'reversal'
		 ORDER BY e.customer_id, e.entry_date, e.id`, on, on)
	if err != nil {
		return nil, fmt.Errorf("read what moved up to %s: %w", on, err)
	}
	defer rows.Close()

	deltas := []Delta{}
	for rows.Next() {
		var d Delta
		if err := rows.Scan(&d.CustomerID, &d.Date, &d.Principal, &d.Interest); err != nil {
			return nil, fmt.Errorf("read a movement up to %s: %w", on, err)
		}
		// A voided entry and its reversal cancel to nothing in both columns. It
		// moved no money in the end, so it is neither a lot nor a payment.
		if d.Principal.IsZero() && d.Interest.IsZero() {
			continue
		}
		deltas = append(deltas, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read what moved up to %s: %w", on, err)
	}
	return deltas, nil
}
