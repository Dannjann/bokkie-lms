// Package ledger is the append-only record of every peso that has moved, and
// the only place a balance comes from.
//
// It knows about entries and balances. It does not know about palay, vouchers,
// receipts or interest rules — those are the steps built on top of it, and each
// of them is a read or a write against this package and nothing more. If a
// lending concept starts appearing in here, the boundary has been crossed.
//
// Three rules hold the whole thing up:
//
//   - Nothing is ever updated or deleted. A void writes a reversing entry, so
//     the record shows that a mistake was made and then undone, rather than
//     showing that it never happened.
//   - Principal and interest are tracked separately and never netted.
//   - A balance is the sum of the entries. customer_balances is a cache, it is
//     rebuildable from scratch at any moment, and it is checked against the sum
//     rather than trusted.
//
// The first and third rules are enforced by the schema as well as by this
// package — see internal/db/migrations/00003_ledger.sql. Application-layer
// discipline is not enough for the one table where a mistake is unrecoverable.
package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// EntryType is what caused an entry. The set is closed, and the database holds
// the same list in a CHECK constraint.
type EntryType string

const (
	// TypeOpening is a balance carried in from the legacy system at cutover.
	// Opening balances are entries rather than columns on the customer, so the
	// ledger is complete from its first day and the first day's reports are
	// right.
	TypeOpening EntryType = "opening"

	// TypeAdvance is money lent: a posted voucher line.
	TypeAdvance EntryType = "advance"

	// TypeInterest is interest charged by an interest run.
	TypeInterest EntryType = "interest"

	// TypePayment is money coming back, whether as cash or as palay.
	TypePayment EntryType = "payment"

	// TypeAdjustment is a manager correcting something that no other entry type
	// describes. It always carries a reason.
	TypeAdjustment EntryType = "adjustment"

	// TypeReversal undoes exactly one earlier entry. It is never constructed by
	// a caller: use Reverse, which is the only thing that can produce one.
	TypeReversal EntryType = "reversal"
)

// Actor is who did something.
//
// UserID is zero when nobody was signed in — an interest run on a schedule, a
// cutover import at the console. The username is recorded either way, for the
// same reason as in the audit log: "console" is a truthful answer to "who", and
// inventing a user id would be a claim the ledger cannot support.
type Actor struct {
	UserID   int64
	Username string
}

// Console is the actor for something done at the host's command line.
var Console = Actor{Username: audit.ConsoleUser}

// Entry is one movement of money against one customer.
//
// Deltas are signed from the lender's point of view: positive increases what
// the customer owes. That is what makes a balance a plain SUM with no case
// analysis anywhere in the system.
type Entry struct {
	ID         int64
	CustomerID int64

	// Date is the business date — the day the money moved. Separate from
	// CreatedAt, which is the moment the row was written; a voucher dated
	// Monday can legitimately be typed in on Tuesday.
	Date Date

	Type      EntryType
	Principal money.Money
	Interest  money.Money

	// SourceTable and SourceID point back at the document a person can be
	// shown. Both set, or neither.
	SourceTable string
	SourceID    int64

	// ReversesID is the entry this one undoes, and is set only on a reversal.
	ReversesID int64

	// Reason is required on an adjustment and on a reversal: those are the two
	// entry types that exist because something went wrong, and the moment of
	// writing is the only moment anyone still knows why.
	Reason string

	CreatedAt time.Time
	By        Actor
}

// IsReversal reports whether this entry undoes another.
func (e Entry) IsReversal() bool { return e.Type == TypeReversal }

// Balance is what a customer owes, with principal and interest kept apart.
type Balance struct {
	CustomerID int64
	Principal  money.Money
	Interest   money.Money

	// LastActivity is the date of the most recent entry, or the zero Date when
	// the customer has none.
	LastActivity Date
}

// Total is principal plus interest. It exists for display; nothing in the
// system stores or reasons about the combined figure, because the question of
// which part a payment clears first only has meaning while they are apart.
func (b Balance) Total() (money.Money, error) {
	return b.Principal.Add(b.Interest)
}

// The errors callers are expected to branch on. Everything else is wrapped
// context around one of these or around a database failure.
var (
	// ErrNotFound means there is no entry with that id.
	ErrNotFound = errors.New("no such ledger entry")

	// ErrAlreadyReversed means the entry has been voided once already. An entry
	// can be reversed at most once.
	ErrAlreadyReversed = errors.New("that entry has already been reversed")

	// ErrReversalOfReversal means someone tried to undo a void. Correcting a
	// mistaken void means posting a fresh entry, which leaves a legible trail
	// of what actually happened rather than a chain nobody can read.
	ErrReversalOfReversal = errors.New("a reversal cannot itself be reversed")

	// ErrInvalidEntry means the entry breaks one of the rules in Validate.
	ErrInvalidEntry = errors.New("invalid ledger entry")
)

// Station is what the ledger needs to know about where it is running: which
// calendar "today" means. settings.Store satisfies it.
//
// It is an interface rather than a *time.Location so that changing the station
// timezone takes effect immediately, the same as every other setting, instead
// of waiting for a restart that on this machine may be months away.
type Station interface {
	Location() *time.Location
}

// Service is the whole ledger API. Every feature from step 4 onward is a call
// into one of these methods; keeping the surface this small is what makes the
// invariants checkable.
type Service struct {
	db      *sql.DB
	station Station
	auditor Auditor
	log     *slog.Logger

	// now is the clock, replaceable in tests so that a reversal's date is
	// something a test can assert on.
	now func() time.Time
}

// New returns a ledger service over database.
//
// The auditor is not optional. The one thing this package does that is not
// recorded by the route that called it is rebuilding a cache it found to be
// wrong, and that is precisely the event somebody will go looking for months
// later when asked why a balance changed.
func New(database *sql.DB, station Station, auditor Auditor, log *slog.Logger) *Service {
	return &Service{
		db:      database,
		station: station,
		auditor: auditor,
		log:     log,
		now:     time.Now,
	}
}

// today is the station's current calendar day.
func (s *Service) today() Date {
	return DateOf(s.now().In(s.station.Location()))
}

// Validate checks an entry against every rule this package can check without
// looking at the database. The schema checks the same things again; this exists
// so a caller gets a sentence it can put on a screen rather than a SQLite
// constraint name.
func (e Entry) Validate() error {
	switch {
	case e.CustomerID <= 0:
		return fmt.Errorf("%w: it names no customer", ErrInvalidEntry)
	case e.Date.IsZero():
		return fmt.Errorf("%w: it has no date", ErrInvalidEntry)
	case e.By.Username == "":
		return fmt.Errorf("%w: it does not say who made it", ErrInvalidEntry)
	case e.Principal.IsZero() && e.Interest.IsZero():
		return fmt.Errorf("%w: it moves no money", ErrInvalidEntry)
	}

	switch e.Type {
	case TypeOpening:
		// A customer can start the new system in credit as easily as in debt,
		// so neither sign is wrong here.
	case TypeAdvance:
		if !e.Principal.IsPositive() || !e.Interest.IsZero() {
			return fmt.Errorf("%w: an advance lends principal and charges no interest, got %s and %s",
				ErrInvalidEntry, e.Principal, e.Interest)
		}
	case TypeInterest:
		if !e.Principal.IsZero() || !e.Interest.IsPositive() {
			return fmt.Errorf("%w: an interest charge touches interest only, got %s and %s",
				ErrInvalidEntry, e.Principal, e.Interest)
		}
	case TypePayment:
		if e.Principal.IsPositive() || e.Interest.IsPositive() {
			return fmt.Errorf("%w: a payment reduces what is owed, got %s and %s",
				ErrInvalidEntry, e.Principal, e.Interest)
		}
	case TypeAdjustment:
		if e.Reason == "" {
			return fmt.Errorf("%w: an adjustment must say why", ErrInvalidEntry)
		}
	case TypeReversal:
		return fmt.Errorf("%w: reversals are written by Reverse, not by Post", ErrInvalidEntry)
	default:
		return fmt.Errorf("%w: %q is not an entry type", ErrInvalidEntry, e.Type)
	}

	if (e.SourceTable == "") != (e.SourceID == 0) {
		return fmt.Errorf("%w: provenance must name both a table and a row, got %q and %d",
			ErrInvalidEntry, e.SourceTable, e.SourceID)
	}
	return nil
}

// Post appends one entry and returns its id.
func (s *Service) Post(ctx context.Context, e Entry) (int64, error) {
	ids, err := s.PostBatch(ctx, []Entry{e})
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

// PostBatch appends several entries in one transaction, returning their ids in
// the order they were given.
//
// This is not an optimisation. A voucher can span several customers, and half a
// posted voucher is not a partial success — it is a corruption that somebody
// has to unpick by hand from the one table that cannot be edited. Either every
// line lands or none does.
func (s *Service) PostBatch(ctx context.Context, entries []Entry) ([]int64, error) {
	var ids []int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		ids, err = s.PostBatchTx(ctx, tx, entries)
		return err
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// PostBatchTx is PostBatch inside a transaction the caller already owns.
//
// A document that produces entries — an advance voucher, and later a payment —
// has a row of its own to write and an audit entry to go with it, and all of
// that has to land together or not at all. A voucher marked posted with no
// entries behind it is not a smaller problem than a failed post: it is a
// document claiming money moved, contradicted by the one table that cannot be
// corrected by editing. So the transaction belongs to whoever writes the
// document, and the ledger joins it rather than opening a second one.
//
// Validation happens here rather than in PostBatch so that both doors are
// guarded by the same check.
func (s *Service) PostBatchTx(ctx context.Context, tx *sql.Tx, entries []Entry) ([]int64, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	for i, e := range entries {
		if err := e.Validate(); err != nil {
			return nil, fmt.Errorf("entry %d of %d: %w", i+1, len(entries), err)
		}
	}
	return s.insert(ctx, tx, entries)
}

// Reverse writes the exact negation of an entry and returns the reversal's id.
//
// The reversal is dated today, not on the day of the entry it undoes. That is
// deliberate: a statement printed in February must still say in March what it
// said in February, and back-dating a correction into a closed period silently
// rewrites a number somebody has already been shown. The correction appears in
// the period it was discovered, which is also how it would be done on paper.
func (s *Service) Reverse(ctx context.Context, entryID int64, by Actor, reason string) (int64, error) {
	var reversalID int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		reversalID, err = s.ReverseTx(ctx, tx, entryID, by, reason)
		return err
	})
	if err != nil {
		return 0, err
	}
	return reversalID, nil
}

// ReverseTx is Reverse inside a transaction the caller already owns.
//
// Voiding a document is the mirror of posting one: the document's status, every
// reversal it implies, and the audit entry all describe a single act. Splitting
// them across two transactions would allow a voucher marked voided whose
// entries are still standing — which reads, to anyone looking at a balance, as
// money that was never repaid.
func (s *Service) ReverseTx(ctx context.Context, tx *sql.Tx, entryID int64, by Actor, reason string) (int64, error) {
	if reason == "" {
		return 0, fmt.Errorf("%w: a reversal must say why", ErrInvalidEntry)
	}
	if by.Username == "" {
		return 0, fmt.Errorf("%w: a reversal must say who made it", ErrInvalidEntry)
	}

	target, err := s.entryTx(ctx, tx, entryID)
	if err != nil {
		return 0, err
	}
	if target.IsReversal() {
		return 0, fmt.Errorf("%w: entry %d", ErrReversalOfReversal, entryID)
	}

	// The unique index makes this race-proof; the check is here so the
	// caller gets a sentence instead of a constraint violation.
	var already int64
	switch err := tx.QueryRowContext(ctx,
		`SELECT id FROM ledger_entries WHERE reverses_id = ?`, entryID).Scan(&already); {
	case err == nil:
		return 0, fmt.Errorf("%w: entry %d was reversed by entry %d", ErrAlreadyReversed, entryID, already)
	case !errors.Is(err, sql.ErrNoRows):
		return 0, fmt.Errorf("look for an existing reversal of entry %d: %w", entryID, err)
	}

	principal, err := target.Principal.Neg()
	if err != nil {
		return 0, fmt.Errorf("negate the principal of entry %d: %w", entryID, err)
	}
	interest, err := target.Interest.Neg()
	if err != nil {
		return 0, fmt.Errorf("negate the interest of entry %d: %w", entryID, err)
	}

	ids, err := s.insert(ctx, tx, []Entry{{
		CustomerID:  target.CustomerID,
		Date:        s.today(),
		Type:        TypeReversal,
		Principal:   principal,
		Interest:    interest,
		SourceTable: target.SourceTable,
		SourceID:    target.SourceID,
		ReversesID:  target.ID,
		Reason:      reason,
		By:          by,
	}})
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

// insert writes entries and brings the cache up to date, inside a caller's
// transaction.
//
// The cache is written here rather than by a background job for one reason: a
// job can be behind, and a balance that is behind is a balance that is wrong.
// Entry and cache commit together or not at all.
func (s *Service) insert(ctx context.Context, tx *sql.Tx, entries []Entry) ([]int64, error) {
	const insertEntry = `
		INSERT INTO ledger_entries
		    (customer_id, entry_date, entry_type,
		     principal_delta_cents, interest_delta_cents,
		     source_table, source_id, reverses_id, reason,
		     created_at, created_by, created_by_username)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	now := s.now().UTC()
	ids := make([]int64, 0, len(entries))
	touched := make(map[int64]Date, len(entries))

	for _, e := range entries {
		result, err := tx.ExecContext(ctx, insertEntry,
			e.CustomerID, e.Date, string(e.Type),
			e.Principal, e.Interest,
			e.SourceTable, nullableID(e.SourceID), nullableID(e.ReversesID), e.Reason,
			now.Format(time.RFC3339Nano), nullableID(e.By.UserID), e.By.Username)
		if err != nil {
			return nil, fmt.Errorf("post a %s entry for customer %d: %w", e.Type, e.CustomerID, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("read the id of a posted entry: %w", err)
		}
		ids = append(ids, id)

		if latest, seen := touched[e.CustomerID]; !seen || e.Date.After(latest) {
			touched[e.CustomerID] = e.Date
		}
	}

	for customerID, latest := range touched {
		if err := s.refreshCache(ctx, tx, customerID, latest, now); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// refreshCache recomputes one customer's cached balance from the ledger.
//
// It re-sums rather than adding the deltas onto whatever was there. Summing is
// a handful of rows behind an index, and it makes the cache self-correcting:
// every post of an entry quietly repairs a row that had drifted, instead of
// carrying the drift forward the way the legacy system's balance fields did.
//
// SQLite's sum() raises on integer overflow rather than turning the total into
// a float, so a sum that cannot be held is an error here and not a number that
// looks fine.
func (s *Service) refreshCache(ctx context.Context, tx *sql.Tx, customerID int64, latest Date, now time.Time) error {
	var principal, interest money.Money
	var lastActivity Date
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(principal_delta_cents), 0),
		       COALESCE(SUM(interest_delta_cents), 0),
		       COALESCE(MAX(entry_date), '')
		  FROM ledger_entries
		 WHERE customer_id = ?`, customerID).Scan(&principal, &interest, &lastActivity)
	if err != nil {
		return fmt.Errorf("sum the ledger for customer %d: %w", customerID, err)
	}
	if lastActivity.IsZero() {
		lastActivity = latest
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO customer_balances
		    (customer_id, principal_cents, interest_cents, last_activity_date, rebuilt_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(customer_id) DO UPDATE SET
		    principal_cents    = excluded.principal_cents,
		    interest_cents     = excluded.interest_cents,
		    last_activity_date = excluded.last_activity_date`,
		customerID, principal, interest, lastActivity, now.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("update the cached balance for customer %d: %w", customerID, err)
	}
	return nil
}

// BalanceFor returns what a customer owes now, from the cache.
//
// A customer with no entries has a zero balance and no cache row; that is not
// an error, it is a customer nothing has happened to yet.
func (s *Service) BalanceFor(ctx context.Context, customerID int64) (Balance, error) {
	return balanceFor(ctx, s.db, customerID)
}

// BalanceForTx is BalanceFor inside a transaction the caller already owns.
//
// A payment needs this: the balance it is divided against must be the one that
// is true at the moment its entry is written, not one read on another
// connection before a second poster's entry landed. The cache is written in the
// same transaction as the entries behind it, so inside a transaction it is
// exactly as current as the ledger is.
func (s *Service) BalanceForTx(ctx context.Context, tx *sql.Tx, customerID int64) (Balance, error) {
	return balanceFor(ctx, tx, customerID)
}

func balanceFor(ctx context.Context, q querier, customerID int64) (Balance, error) {
	balance := Balance{CustomerID: customerID}
	err := q.QueryRowContext(ctx, `
		SELECT principal_cents, interest_cents, COALESCE(last_activity_date, '')
		  FROM customer_balances
		 WHERE customer_id = ?`, customerID).
		Scan(&balance.Principal, &balance.Interest, &balance.LastActivity)
	if errors.Is(err, sql.ErrNoRows) {
		return balance, nil
	}
	if err != nil {
		return Balance{}, fmt.Errorf("read the cached balance for customer %d: %w", customerID, err)
	}
	return balance, nil
}

// BalanceAt returns what a customer owed at the end of a given day, summed from
// the ledger rather than read from the cache.
//
// Reports are historical: "what did this farmer owe at the end of the season"
// is the question, and a service that only knows "now" makes it expensive to
// answer. Building it here, while the ledger is the only thing that exists,
// costs one WHERE clause.
func (s *Service) BalanceAt(ctx context.Context, customerID int64, on Date) (Balance, error) {
	if on.IsZero() {
		return Balance{}, fmt.Errorf("%w: BalanceAt needs a date", ErrInvalidEntry)
	}

	balance := Balance{CustomerID: customerID}
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(principal_delta_cents), 0),
		       COALESCE(SUM(interest_delta_cents), 0),
		       COALESCE(MAX(entry_date), '')
		  FROM ledger_entries
		 WHERE customer_id = ? AND entry_date <= ?`, customerID, on).
		Scan(&balance.Principal, &balance.Interest, &balance.LastActivity)
	if err != nil {
		return Balance{}, fmt.Errorf("sum the ledger for customer %d up to %s: %w", customerID, on, err)
	}
	return balance, nil
}

// StatementFor returns one customer's entries between two dates inclusive, in
// the order they would be printed. A zero from means "since the beginning"; a
// zero to means "up to today".
func (s *Service) StatementFor(ctx context.Context, customerID int64, from, to Date) ([]Entry, error) {
	if to.IsZero() {
		to = s.today()
	}
	if !from.IsZero() && from.After(to) {
		return nil, fmt.Errorf("%w: a statement cannot run from %s back to %s", ErrInvalidEntry, from, to)
	}

	// Ordered by id within a day, which is the order things were typed. Two
	// entries on the same date have no other defensible order, and a stable one
	// matters because this is what gets printed and compared against a slip.
	rows, err := s.db.QueryContext(ctx, selectEntries+`
		 WHERE customer_id = ? AND entry_date >= ? AND entry_date <= ?
		 ORDER BY entry_date, id`, customerID, from, to)
	if err != nil {
		return nil, fmt.Errorf("read the statement for customer %d: %w", customerID, err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the statement for customer %d: %w", customerID, err)
	}
	return entries, nil
}

// EntryByID reads one entry.
func (s *Service) EntryByID(ctx context.Context, id int64) (Entry, error) {
	rows, err := s.db.QueryContext(ctx, selectEntries+` WHERE id = ?`, id)
	if err != nil {
		return Entry{}, fmt.Errorf("read ledger entry %d: %w", id, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Entry{}, fmt.Errorf("read ledger entry %d: %w", id, err)
		}
		return Entry{}, fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	return scanEntry(rows)
}

func (s *Service) entryTx(ctx context.Context, tx *sql.Tx, id int64) (Entry, error) {
	rows, err := tx.QueryContext(ctx, selectEntries+` WHERE id = ?`, id)
	if err != nil {
		return Entry{}, fmt.Errorf("read ledger entry %d: %w", id, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Entry{}, fmt.Errorf("read ledger entry %d: %w", id, err)
		}
		return Entry{}, fmt.Errorf("%w: %d", ErrNotFound, id)
	}
	return scanEntry(rows)
}

// EntriesBySource returns every entry produced by the named rows of one source
// table, oldest first.
//
// This is how a posted document answers "what did I do to the ledger?". A void
// reads it to find what it has to undo; a voucher screen reads it to show what
// posting actually wrote. It belongs here rather than as a join in the
// document's own package because "what came from this document?" is a question
// about entries, and this package is the only thing allowed to answer it.
//
// Reversals come back too. A reversal carries the same provenance as the entry
// it undoes, so a voided document shows both halves of what happened instead of
// a set of entries that appear to be standing.
func (s *Service) EntriesBySource(ctx context.Context, table string, ids []int64) ([]Entry, error) {
	return s.entriesBySource(ctx, s.db, table, ids)
}

// EntriesBySourceTx is EntriesBySource inside a transaction the caller owns —
// which is what voiding needs, because the entries it is about to reverse must
// be read in the same transaction that reverses them.
func (s *Service) EntriesBySourceTx(ctx context.Context, tx *sql.Tx, table string, ids []int64) ([]Entry, error) {
	return s.entriesBySource(ctx, tx, table, ids)
}

func (s *Service) entriesBySource(ctx context.Context, q querier, table string, ids []int64) ([]Entry, error) {
	if table == "" {
		return nil, fmt.Errorf("%w: entries are looked up under a source table, and none was given", ErrInvalidEntry)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	// An IN list built from the ids themselves. The count varies with the
	// document — a voucher has as many lines as it has — so this is one of the
	// few queries in the system that cannot be a constant.
	args := make([]any, 0, len(ids)+1)
	args = append(args, table)
	marks := make([]string, len(ids))
	for i, id := range ids {
		marks[i] = "?"
		args = append(args, id)
	}

	rows, err := q.QueryContext(ctx, selectEntries+`
		 WHERE source_table = ? AND source_id IN (`+strings.Join(marks, ", ")+`)
		 ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("read the entries from %s: %w", table, err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the entries from %s: %w", table, err)
	}
	return entries, nil
}

const selectEntries = `
	SELECT id, customer_id, entry_date, entry_type,
	       principal_delta_cents, interest_delta_cents,
	       source_table, COALESCE(source_id, 0), COALESCE(reverses_id, 0), reason,
	       created_at, COALESCE(created_by, 0), created_by_username
	  FROM ledger_entries`

// scanner is what *sql.Row and *sql.Rows have in common.
type scanner interface{ Scan(dest ...any) error }

// querier is what *sql.DB and *sql.Tx have in common for reads, so one
// implementation serves a caller who has a transaction and one who does not.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func scanEntry(row scanner) (Entry, error) {
	var e Entry
	var entryType, createdAt string
	err := row.Scan(&e.ID, &e.CustomerID, &e.Date, &entryType,
		&e.Principal, &e.Interest,
		&e.SourceTable, &e.SourceID, &e.ReversesID, &e.Reason,
		&createdAt, &e.By.UserID, &e.By.Username)
	if err != nil {
		return Entry{}, fmt.Errorf("read a ledger entry: %w", err)
	}
	e.Type = EntryType(entryType)
	if e.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return Entry{}, fmt.Errorf("ledger entry %d has an unreadable created_at %q: %w", e.ID, createdAt, err)
	}
	return e, nil
}

// nullableID writes SQL NULL for zero. An id of 0 means "there isn't one" —
// storing it as 0 would be a foreign key to a row that cannot exist.
func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// inTx runs fn in a transaction, rolling back on any error or panic.
func (s *Service) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin a ledger transaction: %w", err)
	}
	defer func() {
		// A rollback after a successful commit is a no-op, so this needs no
		// flag to tell the two apart.
		_ = tx.Rollback()
	}()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit a ledger transaction: %w", err)
	}
	return nil
}
