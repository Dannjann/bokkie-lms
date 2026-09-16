// Package advance owns the advance voucher: the document that lends money.
//
// A voucher is the legacy system's "Ref. No. 1425" header with its grid of
// lines beneath it. It is typed as a draft, posted once — which is the moment
// its lines become ledger entries — and undone only by reversing every one of
// those entries at once.
//
// This package imports internal/domain/ledger, and that direction is the
// correct one: an advance is a document that produces ledger entries, so it has
// to know the ledger exists. The ledger knows nothing about vouchers — it takes
// entries carrying a source table's name and never asks what is in that table.
package advance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// SourceTable is the provenance every ledger entry a voucher produces carries.
// It is the line's table rather than the voucher's, so an entry points at the
// row that names an amount and a customer rather than at the header above it.
const SourceTable = "advance_lines"

// Status is where a voucher is in its life. The set is closed, the database
// holds the same list in a CHECK constraint, and a trigger there allows only
// the two transitions below.
//
//	draft ──post──> posted ──void──> voided
//
// There is no un-post and no re-open. Correcting a posted voucher means voiding
// it and entering a new one, which leaves a legible trail of what actually
// happened instead of a row that has quietly been something else before.
type Status string

const (
	// StatusDraft is a voucher being typed. It has no Ref. No., it has moved no
	// money, and it can be edited or deleted outright.
	StatusDraft Status = "draft"

	// StatusPosted is a voucher whose lines are in the ledger. Nothing about it
	// can change again.
	StatusPosted Status = "posted"

	// StatusVoided is a posted voucher whose every entry has been reversed. The
	// voucher and its reversals both stay on the record: what happened is that
	// money moved and then moved back, not that it never moved.
	StatusVoided Status = "voided"
)

// Valid reports whether s is one of the three statuses.
func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusPosted, StatusVoided:
		return true
	default:
		return false
	}
}

func (s Status) String() string { return string(s) }

// Voucher is one advance: a header and the lines beneath it.
type Voucher struct {
	ID int64 `json:"id"`

	// RefNo is the number staff say out loud and write on the slip. It is zero
	// until the voucher is posted, which is when it is allocated.
	RefNo int64 `json:"ref_no"`

	Date   ledger.Date `json:"date"`
	Notes  string      `json:"notes"`
	Status Status      `json:"status"`

	Lines []Line `json:"lines"`

	// Total is the sum of the lines, added up here rather than in the browser.
	// No peso figure in this system is ever added up in JavaScript.
	Total money.Money `json:"total"`

	CreatedAt time.Time `json:"created_at"`
	CreatedBy Actor     `json:"created_by"`

	PostedAt *time.Time `json:"posted_at"`
	PostedBy Actor      `json:"posted_by"`

	VoidedAt   *time.Time `json:"voided_at"`
	VoidedBy   Actor      `json:"voided_by"`
	VoidReason string     `json:"void_reason"`
}

// Line is one row of the grid: this farmer, this amount, this particular.
//
// The customer's code and name travel with it because every screen that shows a
// line shows them, and the alternative is a second round trip per row.
type Line struct {
	ID           int64  `json:"id"`
	CustomerID   int64  `json:"customer_id"`
	CustomerCode string `json:"customer_code"`
	CustomerName string `json:"customer_name"`

	Date       ledger.Date `json:"date"`
	Particular string      `json:"particular"`
	Amount     money.Money `json:"amount"`
}

// Draft is everything a caller is allowed to decide.
//
// It carries no Ref. No. and no status, for the same reason customer.Draft
// carries no code: a rule the type cannot express is a rule somebody has to
// remember. Both are decided by this package — the number when the voucher is
// posted, the status by what has happened to it.
type Draft struct {
	Date  ledger.Date `json:"date"`
	Notes string      `json:"notes"`
	Lines []DraftLine `json:"lines"`
}

// DraftLine is one line as typed. A zero Date means the voucher's date, which
// is what the grid fills in for every row until somebody changes one.
type DraftLine struct {
	CustomerID int64       `json:"customer_id"`
	Date       ledger.Date `json:"date"`
	Particular string      `json:"particular"`
	Amount     money.Money `json:"amount"`
}

// Actor is who did something.
//
// It is the ledger's Actor, aliased rather than redeclared. internal/domain/
// customer goes to some trouble to avoid exactly this, and the difference is
// that this package already imports the ledger in order to post entries: a
// voucher's actor becomes an entry's actor unchanged, so a separate type here
// would exist only to be converted back.
type Actor = ledger.Actor

// ValidationError is a complaint about what somebody typed, as opposed to a
// fault in the system. The messages are written for whoever is standing at the
// counter, because that is who will read them.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string { return e.Message }

var (
	// ErrNotFound means there is no voucher with that id.
	ErrNotFound = errors.New("no such voucher")

	// ErrNotDraft means the voucher has already been posted. Editing, deleting
	// and posting all require a draft, and all three report this.
	ErrNotDraft = errors.New("that voucher has been posted already")

	// ErrNotPosted means only a posted voucher can be voided. A draft is
	// deleted instead — it moved no money, so there is nothing to reverse.
	ErrNotPosted = errors.New("only a posted voucher can be voided")
)

// Limits, generous enough that nobody meets them at a counter by accident and
// tight enough that a malformed request cannot write a hundred thousand ledger
// entries in one transaction.
const (
	maxLines         = 200
	maxParticularLen = 200
	maxNotesLen      = 1000
)

// Station is what this package needs to know about where it is running: which
// calendar "today" means, and where the Ref. No. sequence starts. settings.Store
// satisfies it.
//
// It is an interface rather than the store itself for the same reason the
// ledger's Station is: a setting changed at the station takes effect
// immediately, rather than at a restart which on that machine may be months
// away.
type Station interface {
	Location() *time.Location
	Int(d settings.Def) int
}

// Service is the advance voucher.
type Service struct {
	db      *sql.DB
	ledger  *ledger.Service
	station Station
	log     *slog.Logger

	// now is the clock, replaceable in tests.
	now func() time.Time
}

// New returns an advance service over database.
//
// The ledger is not optional and is not an interface: posting a voucher is a
// ledger write, and a voucher service that could be constructed without one
// would be a document that cannot do the only thing it exists for.
func New(database *sql.DB, entries *ledger.Service, station Station, log *slog.Logger) *Service {
	return &Service{db: database, ledger: entries, station: station, log: log, now: time.Now}
}

// today is the station's current calendar day, which is what "not in the
// future" is measured against.
func (s *Service) today() ledger.Date {
	return ledger.DateOf(s.now().In(s.station.Location()))
}

// queryExecer is satisfied by both *sql.DB and *sql.Tx, so one implementation
// serves a handler holding a transaction and a plain read that is not. The same
// shape as the customer package's.
type queryExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// clean trims, defaults and checks a draft.
//
// It is a method rather than a function on Draft because two of its rules need
// the station's clock: a voucher may be dated any day up to today and no later,
// and a line with no date of its own takes the voucher's.
func (s *Service) clean(d Draft) (Draft, error) {
	today := s.today()

	out := Draft{
		Date:  d.Date,
		Notes: strings.TrimSpace(d.Notes),
	}

	switch {
	case out.Date.IsZero():
		return Draft{}, ValidationError{Field: "date", Message: "a voucher needs a date"}
	case out.Date.After(today):
		// Almost always a wrong year on a January slip. Letting it through puts
		// money in the ledger that has not moved yet, which then turns up on a
		// report as a balance nobody can account for.
		return Draft{}, ValidationError{
			Field:   "date",
			Message: fmt.Sprintf("a voucher cannot be dated in the future — %s is after %s", out.Date, today),
		}
	case len([]rune(out.Notes)) > maxNotesLen:
		return Draft{}, ValidationError{
			Field:   "notes",
			Message: fmt.Sprintf("those notes are too long — %d characters at most", maxNotesLen),
		}
	}

	switch {
	case len(d.Lines) == 0:
		return Draft{}, ValidationError{Field: "lines", Message: "a voucher needs at least one line"}
	case len(d.Lines) > maxLines:
		return Draft{}, ValidationError{
			Field:   "lines",
			Message: fmt.Sprintf("a voucher takes %d lines at most, and this one has %d", maxLines, len(d.Lines)),
		}
	}

	out.Lines = make([]DraftLine, 0, len(d.Lines))
	for i, line := range d.Lines {
		at := fmt.Sprintf("lines[%d]", i)

		cleaned := DraftLine{
			CustomerID: line.CustomerID,
			Date:       line.Date,
			Particular: strings.TrimSpace(line.Particular),
			Amount:     line.Amount,
		}
		if cleaned.Date.IsZero() {
			cleaned.Date = out.Date
		}

		switch {
		case cleaned.CustomerID <= 0:
			return Draft{}, ValidationError{Field: at + ".customer_id", Message: fmt.Sprintf("line %d has no customer", i+1)}
		case !cleaned.Amount.IsPositive():
			// A negative advance is not an advance. Money moving the other way
			// is a payment or an adjustment, and both are documents of their own.
			return Draft{}, ValidationError{
				Field:   at + ".amount",
				Message: fmt.Sprintf("line %d must be for more than nothing", i+1),
			}
		case cleaned.Date.After(today):
			return Draft{}, ValidationError{
				Field:   at + ".date",
				Message: fmt.Sprintf("line %d cannot be dated in the future — %s is after %s", i+1, cleaned.Date, today),
			}
		case len([]rune(cleaned.Particular)) > maxParticularLen:
			return Draft{}, ValidationError{
				Field:   at + ".particular",
				Message: fmt.Sprintf("the particular on line %d is too long — %d characters at most", i+1, maxParticularLen),
			}
		}

		out.Lines = append(out.Lines, cleaned)
	}
	return out, nil
}

// nullableID writes SQL NULL for zero, so that "nobody was signed in" is a NULL
// rather than a foreign key to a user id that cannot exist.
func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
