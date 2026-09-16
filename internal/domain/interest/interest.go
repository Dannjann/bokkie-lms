// Package interest owns the interest run: the document that charges for money
// already lent.
//
// It is the legacy system's "Compute Percentage", and it is the most dangerous
// thing in the application. Every other document is one farmer, one slip, one
// conversation — an advance posted wrong is noticed by the person it was posted
// for. A run is computed from the record rather than typed from paper, against
// every borrower at once, and a run posted wrong is three hundred and sixty-four
// balances that are all slightly wrong and all entirely plausible.
//
// Three things follow from that, and they are the shape of this package:
//
//   - **The draft is the preview.** Not a description of what a run would do:
//     the run itself, minus the commit. A preview computed by different code
//     than the thing it previews is a second implementation that will
//     eventually disagree, in the direction nobody checks.
//   - **Posting recomputes and refuses a stale preview.** A manager approved
//     specific figures. If a payment landed since, the numbers are no longer
//     the ones that were approved, and substituting fresh ones silently while
//     writing three hundred entries is the event this package exists to
//     prevent.
//   - **A period is charged once.** Guarded by an index for the ordinary case
//     and by a check in Go for the overlapping one, because an index cannot
//     express that two ranges intersect.
//
// The rule itself — Q1 of the domain interview, unanswered — is in rule.go, and
// it is the only part of this package an answer can change.
package interest

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

// SourceTable is the provenance every ledger entry a run produces carries. It
// is the line's table rather than the run's, as an advance's is: an entry points
// at the row that names an amount and a customer, not at the header above it.
const SourceTable = "interest_run_lines"

// Status is where a run is in its life. The set is closed, the database holds
// the same list in a CHECK constraint, and a trigger there allows only the two
// transitions below.
//
//	draft ──post──> posted ──void──> voided
//
// There is no un-post and no re-open. Correcting a posted run means voiding it
// and running it again.
type Status string

const (
	// StatusDraft is a run that has been computed and not charged. It has no
	// number, it has written nothing, and it can be recomputed or deleted.
	StatusDraft Status = "draft"

	// StatusPosted is a run whose lines are in the ledger.
	StatusPosted Status = "posted"

	// StatusVoided is a posted run whose every entry has been reversed. Both
	// stay on the record: what happened is that interest was charged and then
	// taken back, not that it was never charged.
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

// Run is one interest run: the period, the rule it was computed under, and a
// line for every farmer it charges.
type Run struct {
	ID int64 `json:"id"`

	// RunNo is zero until the run is posted, which is when it is allocated.
	// Unlike Ref. No. and OR No. it continues no legacy sequence — there was
	// never a pad of paper for this — so there is no *.start setting behind it.
	RunNo int64 `json:"run_no"`

	From ledger.Date `json:"period_from"`
	To   ledger.Date `json:"period_to"`

	// Rule is the rule as it stood when this run was computed, copied onto the
	// row. Changing the settings does not change a run that already exists.
	Rule Rule `json:"rule"`

	Notes  string `json:"notes"`
	Status Status `json:"status"`

	Lines []Line `json:"lines"`

	// Total is the sum of the charges, added up here rather than in the
	// browser. No peso figure in this system is added up in JavaScript, and on
	// this screen the total is the number a manager checks before pressing
	// anything.
	Total money.Money `json:"total"`

	CreatedAt time.Time `json:"created_at"`
	CreatedBy Actor     `json:"created_by"`

	PostedAt *time.Time `json:"posted_at"`
	PostedBy Actor      `json:"posted_by"`

	VoidedAt   *time.Time `json:"voided_at"`
	VoidedBy   Actor      `json:"voided_by"`
	VoidReason string     `json:"void_reason"`
}

// Line is one farmer's charge, and what it was computed from.
//
// Base is stored rather than recomputed because "why is this ₱150.00?" is the
// question a farmer asks, and answering it from a balance that has moved since
// does not answer it.
type Line struct {
	ID           int64  `json:"id"`
	CustomerID   int64  `json:"customer_id"`
	CustomerCode string `json:"customer_code"`
	CustomerName string `json:"customer_name"`

	Base   money.Money `json:"base"`
	Charge money.Money `json:"charge"`
}

// Draft is everything a caller is allowed to decide: which period, and a note
// about why.
//
// No rule, and no lines. The rule comes from the settings and the lines are
// computed from the ledger — a caller that could supply either would be a
// caller that could charge an arbitrary farmer an arbitrary amount, which is
// the one thing a document that writes to every balance at once must not allow.
type Draft struct {
	From  ledger.Date `json:"period_from"`
	To    ledger.Date `json:"period_to"`
	Notes string      `json:"notes"`
}

// Actor is who did something — the ledger's, aliased, as in advance and
// payment: a run's actor becomes its entries' actor unchanged.
type Actor = ledger.Actor

// ValidationError is a complaint about what somebody chose, as opposed to a
// fault in the system.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string { return e.Message }

var (
	// ErrNotFound means there is no run with that id.
	ErrNotFound = errors.New("no such interest run")

	// ErrNotDraft means the run has been posted already. Recomputing, deleting
	// and posting all require a draft, and all three report this.
	ErrNotDraft = errors.New("that interest run has been posted already")

	// ErrNotPosted means only a posted run can be voided. A draft is deleted
	// instead — it charged nothing, so there is nothing to reverse.
	ErrNotPosted = errors.New("only a posted interest run can be voided")

	// ErrPeriodAlreadyCharged means a posted run already covers some of this
	// run's period. It is separate from a validation error because it is not
	// something the caller typed wrong: the period was legal when the draft was
	// made, and somebody else charged it in between.
	ErrPeriodAlreadyCharged = errors.New("that period has already been charged")

	// ErrPreviewStale means the ledger moved between the preview and the post,
	// so the figures a manager approved are no longer the figures that would be
	// written. Recomputing the draft is one click; posting different numbers
	// than were approved is not an option.
	ErrPreviewStale = errors.New("the ledger has moved since this run was computed")

	// ErrNothingToCharge means the run has no lines. A posted run that wrote no
	// entries would be a document claiming something happened when nothing did.
	ErrNothingToCharge = errors.New("this run has nothing to charge")
)

const maxNotesLen = 1000

// Station is what this package needs to know about where it is running: which
// calendar "today" means, and the three settings that make up the rule.
// settings.Store satisfies it.
//
// An interface rather than the store itself so that a rate changed at the
// station takes effect on the next run, rather than at a restart which on that
// machine may be months away.
type Station interface {
	Location() *time.Location
	Int(d settings.Def) int
	Choice(d settings.Def) string
}

// Service is the interest run.
type Service struct {
	db      *sql.DB
	ledger  *ledger.Service
	station Station
	log     *slog.Logger

	// now is the clock, replaceable in tests.
	now func() time.Time
}

// New returns an interest service over database.
//
// The ledger is not optional and is not an interface, for the reason it is
// neither in advance and payment: a run both reads the ledger to compute itself
// and writes it to charge, and a run service without one could do neither.
func New(database *sql.DB, entries *ledger.Service, station Station, log *slog.Logger) *Service {
	return &Service{db: database, ledger: entries, station: station, log: log, now: time.Now}
}

// today is the station's current calendar day, which is what "not in the
// future" is measured against.
func (s *Service) today() ledger.Date {
	return ledger.DateOf(s.now().In(s.station.Location()))
}

// queryExecer is satisfied by both *sql.DB and *sql.Tx, so one implementation
// serves a handler holding a transaction and a plain read that is not.
type queryExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// clean trims, defaults and checks a draft.
//
// A method rather than a function on Draft because one of its rules needs the
// station's clock: a period may end today at the latest. Charging interest for
// a period that has not finished would compute a balance from entries that have
// not been typed in yet, and the number would be wrong in a way nothing later
// corrects.
func (s *Service) clean(d Draft) (Draft, error) {
	today := s.today()

	out := Draft{
		From:  d.From,
		To:    d.To,
		Notes: strings.TrimSpace(d.Notes),
	}

	switch {
	case out.From.IsZero() || out.To.IsZero():
		return Draft{}, ValidationError{Field: "period_from", Message: "a run needs a period, with both ends"}
	case out.From.After(out.To):
		return Draft{}, ValidationError{
			Field:   "period_to",
			Message: fmt.Sprintf("a period cannot run from %s back to %s", out.From, out.To),
		}
	case out.To.After(today):
		return Draft{}, ValidationError{
			Field:   "period_to",
			Message: fmt.Sprintf("a period cannot end in the future — %s is after %s", out.To, today),
		}
	case len([]rune(out.Notes)) > maxNotesLen:
		return Draft{}, ValidationError{
			Field:   "notes",
			Message: fmt.Sprintf("those notes are too long — %d characters at most", maxNotesLen),
		}
	}
	return out, nil
}

// refused turns "the statement matched no rows" into the sentence that explains
// why: either the run is not there, or it is no longer in the status the
// operation needs.
//
// It reads *after* the write that failed to match, never before it — by then
// the statement has already taken the write lock, so the read cannot be the
// lock upgrade PostTx exists to avoid.
func (s *Service) refused(ctx context.Context, q queryExecer, id int64, res sql.Result, want Status) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("interest run %d: %w", id, err)
	}
	if affected > 0 {
		return nil
	}

	var status string
	switch err := q.QueryRowContext(ctx, `SELECT status FROM interest_runs WHERE id = ?`, id).Scan(&status); {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: %d", ErrNotFound, id)
	case err != nil:
		return fmt.Errorf("read the status of interest run %d: %w", id, err)
	}

	if want == StatusPosted {
		return fmt.Errorf("%w: run %d is %s", ErrNotPosted, id, status)
	}
	return fmt.Errorf("%w: run %d is %s", ErrNotDraft, id, status)
}

// nullableID writes SQL NULL for zero, so that "nobody was signed in" is a NULL
// rather than a foreign key to a user id that cannot exist.
func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
