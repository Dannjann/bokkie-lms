// Package customer is the customer master: who the station lends to.
//
// It owns the customers table the way internal/auth owns users and
// internal/domain/ledger owns entries. It knows names, addresses, phone numbers
// and codes — and deliberately nothing about money. A customer's balance is a
// question for the ledger, and the two are composed in the API layer so that
// neither package imports the other.
//
// Two rules shape almost everything here:
//
//   - A code is assigned by this package and is never supplied by a caller.
//     It is the number written on the farmer's paper slip, so changing one
//     would orphan every slip already handed out. Draft, the type a caller
//     fills in, simply has nowhere to put a code.
//   - Nothing is ever deleted. Codes are allocated as a high-water mark, so a
//     deletion would hand a dead customer's code to a living one and two
//     farmers would hold the same code on paper months apart. Deactivation is
//     the only way out, and it is reversible.
//
// See docs/plans/04-customers.md.
package customer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/audit"
)

// Customer is one farmer on the books.
type Customer struct {
	ID   int64  `json:"id"`
	Code string `json:"code"`

	Name    string `json:"name"`
	Address string `json:"address"`
	CellNo  string `json:"cell_no"`
	Notes   string `json:"notes"`

	// BarangayID is zero for a customer with no barangay set, which today
	// means one entered before barangays existed.
	BarangayID   int64  `json:"barangay_id"`
	BarangayName string `json:"barangay_name"`

	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Draft is everything a caller is allowed to decide.
//
// It has no Code field on purpose: "assigned by the server, never editable"
// becomes a property of the type rather than a rule every handler author has to
// remember — the same trick auth.User uses by having no password-hash field.
type Draft struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	CellNo  string `json:"cell_no"`
	Notes   string `json:"notes"`

	// BarangayID decides the code on a new customer: its prefix and its own
	// sequence. It is required once any barangay exists; before that, zero
	// continues the legacy BA- sequence every earlier customer used. Changing
	// it later moves the farmer without touching the code on their slips.
	BarangayID int64 `json:"barangay_id"`
}

// Actor is who did something.
//
// It is declared here rather than borrowed from internal/domain/ledger. It is
// the same two fields, and the duplication is the point: reusing the ledger's
// type would make this package import the ledger in order to record who typed a
// name, which is precisely the dependency the package boundary exists to avoid.
type Actor struct {
	UserID   int64
	Username string
}

// Console is the actor for something done at the host's command line, which is
// how the cutover import runs.
var Console = Actor{Username: audit.ConsoleUser}

// ValidationError is a complaint about what somebody typed, as opposed to a
// fault in the system. The messages are written for the person at the counter,
// because that is who will read them.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string { return e.Message }

var (
	// ErrNotFound means there is no customer with that id.
	ErrNotFound = errors.New("no such customer")

	// ErrCodeTaken means a code is already on the books. Only the import can
	// provoke this, since every other path has its code assigned for it.
	ErrCodeTaken = errors.New("that customer code is already in use")

	// ErrCodeSpaceExhausted means a prefix's sequence has reached its end.
	// Widening the format is a deliberate migration, not something to discover
	// at the counter — see the comment on maxCodeNumber.
	ErrCodeSpaceExhausted = errors.New(
		"this barangay's code sequence is full at 9999; widening it needs a migration")
)

// The code format: a barangay's prefix, a dash, and exactly four zero-padded
// digits. Fixed width within a prefix is what makes lexicographic order and
// numeric order the same thing, which in turn is what lets MAX() be the
// allocator and ORDER BY code be a sensible listing.
const (
	// codePrefix is the legacy sequence, for customers with no barangay.
	codePrefix = "BA-"

	// maxCodeNumber is the end of the sequence. Allocating past it would
	// produce BA-10000, which sorts *before* BA-9999 and would quietly break
	// the allocator, every listing and every report at once. At 364 customers
	// this is decades away; refusing costs one comparison.
	maxCodeNumber = 9999
)

var codePattern = regexp.MustCompile(`^[A-Z]{2,4}-\d{4}$`)

// Field limits. Generous enough that nobody at a counter meets them by
// accident, tight enough that a paste of an entire document is refused.
const (
	maxNameLen    = 120
	maxAddressLen = 200
	maxCellLen    = 32
	maxNotesLen   = 1000
)

// Service is the customer master.
type Service struct {
	db      *sql.DB
	auditor Auditor
	log     *slog.Logger

	// now is the clock, replaceable in tests.
	now func() time.Time
}

// Auditor is the part of internal/audit this package needs. An import writes
// its own audit entry inside its own transaction, which is the one thing here
// the calling route cannot record for us: a route wrapper cannot tell a preview
// from a real import.
type Auditor interface {
	RecordTx(ctx context.Context, tx *sql.Tx, e audit.Entry) error
}

// New returns a customer service over database.
func New(database *sql.DB, auditor Auditor, log *slog.Logger) *Service {
	return &Service{db: database, auditor: auditor, log: log, now: time.Now}
}

// queryExecer is satisfied by both *sql.DB and *sql.Tx, so one implementation
// serves a request handler holding a transaction and the import holding its
// own. The same shape as auth's.
type queryExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// clean trims, normalises and checks a draft. Normalisation happens here rather
// than in a handler so that every path into the table — counter, API, import —
// stores the same thing.
func (d Draft) clean() (Draft, error) {
	out := Draft{
		Name:    strings.TrimSpace(d.Name),
		Address: strings.TrimSpace(d.Address),
		CellNo:  NormaliseCell(d.CellNo),
		Notes:   strings.TrimSpace(d.Notes),

		BarangayID: d.BarangayID,
	}

	switch {
	case out.Name == "":
		return Draft{}, ValidationError{Field: "name", Message: "a customer needs a name"}
	case len([]rune(out.Name)) > maxNameLen:
		return Draft{}, ValidationError{
			Field:   "name",
			Message: fmt.Sprintf("that name is too long — %d characters at most", maxNameLen),
		}
	case len([]rune(out.Address)) > maxAddressLen:
		return Draft{}, ValidationError{
			Field:   "address",
			Message: fmt.Sprintf("that address is too long — %d characters at most", maxAddressLen),
		}
	case len(out.CellNo) > maxCellLen:
		return Draft{}, ValidationError{
			Field:   "cell_no",
			Message: "that does not look like a phone number",
		}
	case len([]rune(out.Notes)) > maxNotesLen:
		return Draft{}, ValidationError{
			Field:   "notes",
			Message: fmt.Sprintf("those notes are too long — %d characters at most", maxNotesLen),
		}
	}
	return out, nil
}

// NormaliseCell reduces a phone number to digits, keeping a leading +.
//
// A number typed 0917-123-4567 at one counter and 0917 123 4567 at another is
// the same number, and storing them differently means a search for one does not
// find the other. Validation is otherwise deliberately loose: strict phone
// rules are a nuisance at a counter and catch nothing that matters here.
func NormaliseCell(s string) string {
	var b strings.Builder
	for i, r := range strings.TrimSpace(s) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' && i == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ValidateCode checks a code against the PREFIX-#### format.
//
// Only the import calls this: every other path has its code assigned by the
// allocator and cannot produce a malformed one. A legacy code shaped otherwise
// is rejected rather than accommodated, because it would be invisible to the
// high-water scan and would break the allocator the first time somebody added a
// customer afterwards.
func ValidateCode(code string) error {
	if !codePattern.MatchString(code) {
		return ValidationError{
			Field:   "code",
			Message: fmt.Sprintf("%q is not a customer code — codes look like BA-0001 or PI-0001", code),
		}
	}
	return nil
}

// NormaliseCode trims and upper-cases a code.
//
// The column is COLLATE NOCASE UNIQUE but GLOB is case-sensitive, so a
// lower-case ba-0365 in an import file would slip past the high-water scan and
// then collide on the unique index. Upper-casing on the way in closes that.
func NormaliseCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// CreateTx adds a customer and assigns its code.
//
// The code is allocated *inside* the INSERT rather than by reading MAX and then
// writing. internal/db opens transactions deferred — there is no
// _txlock=immediate — so two encoders clicking Save at the same moment would
// both read 364 and both try to write BA-0365. One statement removes the window
// entirely, and the UNIQUE index on code is the backstop if it is ever
// reintroduced.
//
// Each prefix is its own sequence: PI-0001 and CMG-0001 can both exist. The
// prefix is read from the barangay inside the same statement — an active one,
// or the legacy BA when none is given — so choosing a barangay adds no read
// before the write.
//
// The trailing WHERE is the end-of-sequence guard: past 9999 it matches no
// row, the insert affects nothing, and that is reported rather than allowed to
// become PI-10000. No row also means the barangay does not exist or has been
// retired, which is told apart after the fact.
func (s *Service) CreateTx(ctx context.Context, tx *sql.Tx, d Draft) (Customer, error) {
	draft, err := d.clean()
	if err != nil {
		return Customer{}, err
	}

	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)

	// No barangay is allowed only while there are none to pick: the legacy BA
	// sequence is for the station before barangays, not a way around them.
	source := `SELECT NULL AS bid, '` + strings.TrimSuffix(codePrefix, "-") + `' AS prefix
	            WHERE NOT EXISTS (SELECT 1 FROM barangays WHERE active = 1)`
	args := []any{}
	if draft.BarangayID > 0 {
		source = `SELECT id AS bid, prefix FROM barangays WHERE id = ? AND active = 1`
		args = append(args, draft.BarangayID)
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO customers (code, barangay_id, name, address, cell_no, active, notes, created_at, updated_at)
		 SELECT printf('%s-%04d', seq.prefix, seq.next), seq.bid, ?, ?, ?, 1, ?, ?, ?
		   FROM (SELECT src.bid, src.prefix,
		                (SELECT COALESCE(MAX(CAST(substr(c.code, length(src.prefix) + 2) AS INTEGER)), 0) + 1
		                   FROM customers c
		                  WHERE c.code GLOB src.prefix || '-[0-9][0-9][0-9][0-9]') AS next
		           FROM (`+source+`) AS src) AS seq
		  WHERE seq.next <= ?`,
		append([]any{draft.Name, draft.Address, draft.CellNo, draft.Notes, stamp, stamp},
			append(args, maxCodeNumber)...)...)
	if err != nil {
		return Customer{}, fmt.Errorf("create customer: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return Customer{}, fmt.Errorf("create customer: %w", err)
	}
	if affected == 0 {
		if draft.BarangayID == 0 {
			var any int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM barangays WHERE active = 1`).Scan(&any); err == nil && any > 0 {
				return Customer{}, ValidationError{Field: "barangay_id", Message: "pick the customer's barangay"}
			}
		}
		if draft.BarangayID > 0 {
			b, err := barangayByID(ctx, tx, draft.BarangayID)
			if errors.Is(err, ErrBarangayNotFound) || (err == nil && !b.Active) {
				return Customer{}, ValidationError{Field: "barangay_id", Message: "pick a barangay from the list"}
			}
		}
		return Customer{}, ErrCodeSpaceExhausted
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Customer{}, fmt.Errorf("create customer: %w", err)
	}
	return byID(ctx, tx, id)
}

// UpdateTx changes the editable fields. The code, the id and created_at are
// fixed at creation and are not among them.
func (s *Service) UpdateTx(ctx context.Context, tx *sql.Tx, id int64, d Draft) (Customer, error) {
	draft, err := d.clean()
	if err != nil {
		return Customer{}, err
	}

	// The barangay may change; the code never does. A barangay id that does not
	// exist is caught by the foreign key.
	res, err := tx.ExecContext(ctx,
		`UPDATE customers SET name = ?, address = ?, cell_no = ?, notes = ?, barangay_id = NULLIF(?, 0), updated_at = ?
		  WHERE id = ?`,
		draft.Name, draft.Address, draft.CellNo, draft.Notes, draft.BarangayID,
		s.now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "foreign key") {
			return Customer{}, ValidationError{Field: "barangay_id", Message: "pick a barangay from the list"}
		}
		return Customer{}, fmt.Errorf("update customer %d: %w", id, err)
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return Customer{}, ErrNotFound
	}
	return byID(ctx, tx, id)
}

// SetActiveTx deactivates or reactivates a customer. This is as close to a
// deletion as the system gets, which is why the route that reaches it is
// manager-only.
func (s *Service) SetActiveTx(ctx context.Context, tx *sql.Tx, id int64, active bool) (Customer, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE customers SET active = ?, updated_at = ? WHERE id = ?`,
		boolToInt(active), s.now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return Customer{}, fmt.Errorf("set customer %d active: %w", id, err)
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return Customer{}, ErrNotFound
	}
	return byID(ctx, tx, id)
}

// ByID loads one customer.
func (s *Service) ByID(ctx context.Context, id int64) (Customer, error) {
	return byID(ctx, s.db, id)
}

const selectColumns = `id, code, name, address, cell_no, active, notes, created_at, updated_at,
	COALESCE(barangay_id, 0),
	COALESCE((SELECT b.name FROM barangays b WHERE b.id = customers.barangay_id), '')`

func byID(ctx context.Context, db queryExecer, id int64) (Customer, error) {
	row := db.QueryRowContext(ctx,
		`SELECT `+selectColumns+` FROM customers WHERE id = ?`, id)

	c, err := scanCustomer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Customer{}, ErrNotFound
	}
	if err != nil {
		return Customer{}, fmt.Errorf("load customer %d: %w", id, err)
	}
	return c, nil
}

type scanner interface{ Scan(dest ...any) error }

func scanCustomer(row scanner) (Customer, error) {
	var (
		c                    Customer
		active               int
		createdAt, updatedAt string
	)
	if err := row.Scan(&c.ID, &c.Code, &c.Name, &c.Address, &c.CellNo,
		&active, &c.Notes, &createdAt, &updatedAt, &c.BarangayID, &c.BarangayName); err != nil {
		return Customer{}, err
	}
	c.Active = active == 1
	c.CreatedAt = parseTime(createdAt)
	c.UpdatedAt = parseTime(updatedAt)
	return c, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// parseTime reads a stored RFC3339 timestamp. Everything written to these
// columns went through Format, so a parse failure means somebody hand-edited
// the file; a zero time reads as obviously wrong on screen, which is right.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// isUniqueViolation recognises a duplicate code without depending on the
// driver's error type, the same way auth does for usernames.
func isUniqueViolation(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}
