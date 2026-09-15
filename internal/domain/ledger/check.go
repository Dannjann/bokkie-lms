package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// Auditor is the part of the audit log the ledger uses.
//
// Only the transactional form is here, deliberately. A rebuild of the cache and
// the record that it happened must commit together: a rebuild with no audit row
// is a silent correction, and an audit row for a rebuild that rolled back is a
// lie about the numbers.
type Auditor interface {
	RecordTx(ctx context.Context, tx *sql.Tx, e audit.Entry) error
}

// SelfCheckInlineLimit is how many ledger entries the boot-time check will scan
// before it gives up on being instant and leaves the work to the daily one.
//
// Summing a few hundred thousand integers behind an index is milliseconds, and
// for the first several years of this station's life the whole ledger will be
// far below this. The limit exists so that startup stays fast in year ten
// rather than because the check is expensive today.
const SelfCheckInlineLimit = 200_000

// checkInterval is how often the running server re-checks. Daily is chosen
// against the failure it is looking for: "the numbers have been quietly wrong
// for a while" is measured in days of disputed statements, not in minutes.
const checkInterval = 24 * time.Hour

// Discrepancy is one customer whose cached balance disagreed with the ledger.
type Discrepancy struct {
	CustomerID int64

	CachedPrincipal money.Money
	CachedInterest  money.Money

	LedgerPrincipal money.Money
	LedgerInterest  money.Money
}

func (d Discrepancy) String() string {
	return fmt.Sprintf("customer %d: cache said %s/%s, the ledger says %s/%s",
		d.CustomerID, d.CachedPrincipal, d.CachedInterest, d.LedgerPrincipal, d.LedgerInterest)
}

// EntryCount is how many entries the ledger holds.
func (s *Service) EntryCount(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ledger_entries`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count ledger entries: %w", err)
	}
	return count, nil
}

// Discrepancies compares every cached balance against a fresh sum of the
// ledger, and changes nothing.
//
// Given that the cache is written in the same transaction as the entry that
// changes it, this should always come back empty. It is checked anyway, because
// the cost is negligible and the fault it catches — a balance that has been
// quietly wrong for months — is exactly what the legacy system's carry-forward
// balance fields did, and nobody noticed until a farmer did.
func (s *Service) Discrepancies(ctx context.Context) ([]Discrepancy, error) {
	type pair struct {
		principal money.Money
		interest  money.Money
	}

	derived := map[int64]pair{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT customer_id, SUM(principal_delta_cents), SUM(interest_delta_cents)
		  FROM ledger_entries
		 GROUP BY customer_id`)
	if err != nil {
		return nil, fmt.Errorf("sum the ledger: %w", err)
	}
	for rows.Next() {
		var id int64
		var p pair
		if err := rows.Scan(&id, &p.principal, &p.interest); err != nil {
			rows.Close()
			return nil, fmt.Errorf("sum the ledger: %w", err)
		}
		derived[id] = p
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sum the ledger: %w", err)
	}

	cached := map[int64]pair{}
	rows, err = s.db.QueryContext(ctx,
		`SELECT customer_id, principal_cents, interest_cents FROM customer_balances`)
	if err != nil {
		return nil, fmt.Errorf("read the cached balances: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var p pair
		if err := rows.Scan(&id, &p.principal, &p.interest); err != nil {
			return nil, fmt.Errorf("read the cached balances: %w", err)
		}
		cached[id] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the cached balances: %w", err)
	}

	// Both directions matter. A cache row that is missing is as wrong as one
	// that holds the wrong number, and a cache row for a customer with no
	// entries at all means something deleted from a table that cannot be
	// deleted from.
	var found []Discrepancy
	for id, want := range derived {
		if got := cached[id]; got != want {
			found = append(found, Discrepancy{
				CustomerID:      id,
				CachedPrincipal: got.principal, CachedInterest: got.interest,
				LedgerPrincipal: want.principal, LedgerInterest: want.interest,
			})
		}
	}
	for id, got := range cached {
		if _, inLedger := derived[id]; inLedger {
			continue
		}
		if got != (pair{}) {
			found = append(found, Discrepancy{
				CustomerID:      id,
				CachedPrincipal: got.principal, CachedInterest: got.interest,
			})
		}
	}
	return found, nil
}

// RebuildCache throws customer_balances away and recomputes every row from the
// ledger. It returns the number of customers the rebuilt cache covers.
//
// This is always safe to run: the cache holds nothing the ledger does not, and
// this method is the proof of that claim rather than a repair tool.
func (s *Service) RebuildCache(ctx context.Context) (int, error) {
	var rebuilt int
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		rebuilt, err = s.rebuildTx(ctx, tx)
		return err
	})
	return rebuilt, err
}

func (s *Service) rebuildTx(ctx context.Context, tx *sql.Tx) (int, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM customer_balances`); err != nil {
		return 0, fmt.Errorf("clear the cached balances: %w", err)
	}

	// SQLite's sum() raises on integer overflow rather than quietly turning the
	// total into a float, which is the one behaviour that makes doing this in
	// SQL rather than in Go acceptable for a money column.
	result, err := tx.ExecContext(ctx, `
		INSERT INTO customer_balances
		    (customer_id, principal_cents, interest_cents, last_activity_date, rebuilt_at)
		SELECT customer_id,
		       SUM(principal_delta_cents),
		       SUM(interest_delta_cents),
		       MAX(entry_date),
		       ?
		  FROM ledger_entries
		 GROUP BY customer_id`, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("rebuild the cached balances: %w", err)
	}
	rebuilt, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rebuild the cached balances: %w", err)
	}
	return int(rebuilt), nil
}

// SelfCheck compares the cache against the ledger and, if they disagree, says
// so loudly, rebuilds, and records that it happened.
//
// It returns the discrepancies it found and corrected, so a caller can decide
// whether to do anything further. An empty result is the expected one.
func (s *Service) SelfCheck(ctx context.Context, by Actor) ([]Discrepancy, error) {
	found, err := s.Discrepancies(ctx)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		s.log.Info("ledger cache agrees with the ledger")
		return nil, nil
	}

	// Error, not warn. A balance that disagreed with the ledger means something
	// wrote one without the other, and whoever reads this log needs to find out
	// what — the rebuild below fixes the number, not the cause.
	affected := make([]int64, 0, len(found))
	for _, d := range found {
		affected = append(affected, d.CustomerID)
		s.log.Error("cached balance did not match the ledger", "detail", d.String())
	}
	s.log.Error("rebuilding the ledger cache",
		"customers_affected", len(found), "customer_ids", affected)

	err = s.inTx(ctx, func(tx *sql.Tx) error {
		rebuilt, err := s.rebuildTx(ctx, tx)
		if err != nil {
			return err
		}
		details := make([]string, 0, len(found))
		for _, d := range found {
			details = append(details, d.String())
		}
		return s.auditor.RecordTx(ctx, tx, audit.Entry{
			At:       s.now(),
			UserID:   by.UserID,
			Username: by.Username,
			Action:   audit.ActionLedgerCacheRebuilt,
			Entity:   "customer_balances",
			After: map[string]any{
				"customers_affected": len(found),
				"customers_rebuilt":  rebuilt,
				"discrepancies":      details,
			},
		})
	})
	if err != nil {
		return found, err
	}
	return found, nil
}

// CheckOnStart is the boot-time half of the self-check.
//
// On a small ledger it runs before the server accepts a request, so a corrupted
// cache is corrected before anyone can be shown a wrong number. On a large one
// it stands aside and leaves the work to the daily Checker, because holding up
// the station's startup to re-add ten years of entries would be a worse trade
// than waiting a few minutes for the same answer.
func (s *Service) CheckOnStart(ctx context.Context) error {
	count, err := s.EntryCount(ctx)
	if err != nil {
		return err
	}
	if count > SelfCheckInlineLimit {
		s.log.Info("ledger is large; leaving the cache check to the daily one",
			"entries", count, "inline_limit", SelfCheckInlineLimit)
		return nil
	}
	_, err = s.SelfCheck(ctx, Console)
	return err
}

// Checker re-runs the self-check on a running server.
//
// A station stays up for months at a time, so a check that only happens at
// startup is a check that happens twice a year. This is the schedule that makes
// "the numbers have been quietly wrong for a while" a thing somebody finds out
// about the next morning.
type Checker struct {
	svc *Service
	log *slog.Logger

	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
	started atomic.Bool
}

// NewChecker builds the daily check.
func NewChecker(svc *Service, log *slog.Logger) *Checker {
	return &Checker{
		svc:  svc,
		log:  log,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
}

// Start begins the schedule in the background. Calling it twice is harmless.
func (c *Checker) Start() {
	if c.started.Swap(true) {
		return
	}
	go c.loop()
}

// Stop ends the schedule and waits for a check in flight to finish. It returns
// immediately if the schedule was never started, so an App whose Start failed
// can still be Stopped.
func (c *Checker) Stop() {
	if !c.started.Load() {
		return
	}
	c.once.Do(func() { close(c.stop) })
	<-c.done
}

func (c *Checker) loop() {
	defer close(c.done)

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			if _, err := c.svc.SelfCheck(ctx, Console); err != nil {
				c.log.Error("the ledger cache check could not run", "error", err)
			}
			cancel()
		}
	}
}
