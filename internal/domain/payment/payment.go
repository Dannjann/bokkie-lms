// Package payment owns the receipt: the document that takes money back.
//
// Where an advance voucher is a header with a grid of lines beneath it, a
// payment is a header and nothing else — one farmer at the counter handing
// money over and being given a receipt. That is the one place this package
// deliberately does not mirror internal/domain/advance: the legacy advances
// screen has a grid and the legacy payments screen does not, and a receipt
// covering three payers is not a thing that happens.
//
// A payment is typed as a draft, posted once — which is the moment it becomes a
// ledger entry — and undone only by reversing that entry.
//
// What this package does *not* store is the division of a payment between
// principal and interest. The entry carries both deltas, so the split lives
// there and is read back through the ledger. A copy of a number is a number
// that can disagree with the original.
package payment

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

// SourceTable is the provenance every ledger entry a payment produces carries.
//
// It is the receipt itself rather than a line table, because unlike a voucher
// there is no row beneath the header — which is also why source_id is the
// payment's own id.
const SourceTable = "payments"

// Status is where a payment is in its life. The set is closed, the database
// holds the same list in a CHECK constraint, and a trigger there allows only
// the two transitions below.
//
//	draft ──post──> posted ──void──> voided
//
// There is no un-post and no re-open. Correcting a posted payment means voiding
// it and entering a new one.
type Status string

const (
	// StatusDraft is a payment being typed. It has no OR No., it has moved no
	// money, and it can be edited or deleted outright.
	StatusDraft Status = "draft"

	// StatusPosted is a payment whose entry is in the ledger. Nothing about it
	// can change again.
	StatusPosted Status = "posted"

	// StatusVoided is a posted payment whose entry has been reversed. Both stay
	// on the record: what happened is that money came in and then went back
	// out, not that it never came in.
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

// Method is how the money arrived.
//
// It records the method and not the mechanics. Q2 of the domain interview —
// whether farmers repay in cash or by delivering palay netted against the
// advance — is unanswered, and this is what makes it answerable from real data
// later. If the answer turns out to be "the delivery is valued here", what that
// needs is a delivery document producing a peso figure that feeds a payment,
// not kilos and a price bolted onto this one.
type Method string

const (
	MethodCash  Method = "cash"
	MethodPalay Method = "palay"
	MethodOther Method = "other"
)

// Methods is every method, in the order a screen should offer them.
var Methods = []Method{MethodCash, MethodPalay, MethodOther}

// Valid reports whether m is one of the three methods.
func (m Method) Valid() bool {
	for _, known := range Methods {
		if m == known {
			return true
		}
	}
	return false
}

func (m Method) String() string { return string(m) }

// Rule is which half of a debt a payment clears first.
//
// It comes from the payment.applies setting rather than from a constant because
// it is Q5 of the domain interview and Q5 is unanswered. Nothing charges
// interest until step 7, so no payment posted before then can be affected by
// the answer — which is exactly why settling on a default costs nothing now and
// would cost a migration later.
type Rule string

const (
	InterestFirst  Rule = "interest-first"
	PrincipalFirst Rule = "principal-first"
)

// Payment is one receipt.
type Payment struct {
	ID int64 `json:"id"`

	// OrNo is the number on the receipt staff hand over. It is zero until the
	// payment is posted, which is when it is allocated.
	OrNo int64 `json:"or_no"`

	CustomerID   int64  `json:"customer_id"`
	CustomerCode string `json:"customer_code"`
	CustomerName string `json:"customer_name"`

	Date   ledger.Date `json:"date"`
	Amount money.Money `json:"amount"`
	Method Method      `json:"method"`

	Particular string `json:"particular"`
	Status     Status `json:"status"`

	CreatedAt time.Time `json:"created_at"`
	CreatedBy Actor     `json:"created_by"`

	PostedAt *time.Time `json:"posted_at"`
	PostedBy Actor      `json:"posted_by"`

	VoidedAt   *time.Time `json:"voided_at"`
	VoidedBy   Actor      `json:"voided_by"`
	VoidReason string     `json:"void_reason"`
}

// Draft is everything a caller is allowed to decide.
//
// No OR No. and no status, for the same reason advance.Draft carries no Ref.
// No.: a rule the type cannot express is a rule somebody has to remember.
type Draft struct {
	CustomerID int64       `json:"customer_id"`
	Date       ledger.Date `json:"date"`
	Amount     money.Money `json:"amount"`
	Method     Method      `json:"method"`
	Particular string      `json:"particular"`
}

// Actor is who did something — the ledger's, aliased, as in the advance
// package: a payment's actor becomes its entry's actor unchanged.
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
	// ErrNotFound means there is no payment with that id.
	ErrNotFound = errors.New("no such payment")

	// ErrNotDraft means the payment has already been posted. Editing, deleting
	// and posting all require a draft, and all three report this.
	ErrNotDraft = errors.New("that payment has been posted already")

	// ErrNotPosted means only a posted payment can be voided. A draft is
	// deleted instead — it moved no money, so there is nothing to reverse.
	ErrNotPosted = errors.New("only a posted payment can be voided")
)

const maxParticularLen = 200

// Station is what this package needs to know about where it is running: which
// calendar "today" means, where the OR No. sequence starts, and which half of a
// debt a payment clears first. settings.Store satisfies it.
//
// An interface rather than the store itself so that a setting changed at the
// station takes effect immediately, rather than at a restart which on that
// machine may be months away.
type Station interface {
	Location() *time.Location
	Int(d settings.Def) int
	Choice(d settings.Def) string
}

// Service is the payment receipt.
type Service struct {
	db      *sql.DB
	ledger  *ledger.Service
	station Station
	log     *slog.Logger

	// now is the clock, replaceable in tests.
	now func() time.Time
}

// New returns a payment service over database.
//
// The ledger is not optional, for the reason it is not optional to the advance
// service: posting a payment is a ledger write, and a service that could be
// built without one would be a document unable to do the only thing it exists
// for.
func New(database *sql.DB, entries *ledger.Service, station Station, log *slog.Logger) *Service {
	return &Service{db: database, ledger: entries, station: station, log: log, now: time.Now}
}

// today is the station's current calendar day, which is what "not in the
// future" is measured against.
func (s *Service) today() ledger.Date {
	return ledger.DateOf(s.now().In(s.station.Location()))
}

// rule is the station's payment application order.
func (s *Service) rule() Rule {
	if Rule(s.station.Choice(settings.PaymentApplies)) == PrincipalFirst {
		return PrincipalFirst
	}
	return InterestFirst
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
// station's clock: a payment may be dated any day up to today and no later.
func (s *Service) clean(d Draft) (Draft, error) {
	today := s.today()

	out := Draft{
		CustomerID: d.CustomerID,
		Date:       d.Date,
		Amount:     d.Amount,
		Method:     d.Method,
		Particular: strings.TrimSpace(d.Particular),
	}
	if out.Date.IsZero() {
		out.Date = today
	}
	if out.Method == "" {
		out.Method = MethodCash
	}

	switch {
	case out.CustomerID <= 0:
		return Draft{}, ValidationError{Field: "customer_id", Message: "a payment needs a customer"}
	case !out.Amount.IsPositive():
		// Money moving the other way is an advance or an adjustment, and both
		// are documents of their own.
		return Draft{}, ValidationError{Field: "amount", Message: "a payment must be for more than nothing"}
	case out.Date.After(today):
		// Almost always a wrong year on a January receipt. Letting it through
		// puts money in the ledger that has not moved yet, which then turns up
		// on a report as a balance nobody can account for.
		return Draft{}, ValidationError{
			Field:   "date",
			Message: fmt.Sprintf("a payment cannot be dated in the future — %s is after %s", out.Date, today),
		}
	case !out.Method.Valid():
		return Draft{}, ValidationError{
			Field:   "method",
			Message: fmt.Sprintf("%q is not a way money arrives here — cash, palay or other", out.Method),
		}
	case len([]rune(out.Particular)) > maxParticularLen:
		return Draft{}, ValidationError{
			Field:   "particular",
			Message: fmt.Sprintf("that particular is too long — %d characters at most", maxParticularLen),
		}
	}
	return out, nil
}

// checkCustomer reports whether the payment names somebody on the books.
//
// **A deactivated customer may still pay, and this is where step 5's rule
// inverts on purpose.** advance.checkCustomers refuses to put a deactivated
// farmer on a draft voucher, because deactivation says who the station lends to
// today. It says nothing about who owes money from last season — and a farmer
// deactivated because the relationship ended is the one most likely to be
// settling up. Refusing their payment would leave a debt on the books with no
// way to clear it but an adjustment, which is a correction entry standing in
// for an ordinary event.
//
// Somebody who is not on the books at all is still refused: that is a typed id
// pointing at nothing, not a business decision.
func checkCustomer(ctx context.Context, q queryExecer, customerID int64) error {
	var exists int
	switch err := q.QueryRowContext(ctx,
		`SELECT 1 FROM customers WHERE id = ?`, customerID).Scan(&exists); {
	case errors.Is(err, sql.ErrNoRows):
		return ValidationError{Field: "customer_id", Message: "that customer is not on the books"}
	case err != nil:
		return fmt.Errorf("check the customer on a payment: %w", err)
	}
	return nil
}

// nullableID writes SQL NULL for zero, so that "nobody was signed in" is a NULL
// rather than a foreign key to a user id that cannot exist.
func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
