// Package payment owns the payment: what a farmer brings in to pay down what
// they owe — palay, almost always, and occasionally cash.
//
// A payment is one farmer. A palay payment has a line per lot (class, sacks,
// kilos, price per kilo) and its amount is the sum of their costs; a cash
// payment is an amount typed. Both are typed as a draft and posted once, which
// gives the payment its OR No. and **records** it.
//
// **Recording a payment does not move the balance.** That is the legacy
// system's rule, and it was kept on purpose (docs/plans/07.5-settlement.md): a
// delivery sits recorded until the farmer is settled, and the settlement is
// what charges the interest to the cut-off and then applies the delivery,
// interest first. The ledger entry a payment produces is therefore written by
// internal/domain/settlement, not here — sourced to this payment, so a
// statement still reads "OR No. 812".
//
// What this package does *not* store is the division of a payment between
// principal and interest. The entry carries both deltas, so the split lives
// there and is read back through the ledger.
package payment

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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

// Method is how the payment arrived. A palay payment carries lines and its
// amount is their sum; cash and other carry an amount and no lines.
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

// Rule is which half of a debt a payment clears first. It comes from the
// payment.applies setting; the legacy system cleared interest first in every
// one of its 1,097 settlements, which is the default.
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

	// Lines are the lots of palay, empty for cash.
	Lines []Line `json:"lines"`

	Particular string `json:"particular"`
	Status     Status `json:"status"`

	// SettlementID and SettlementNo name the posted settlement that applied
	// this payment to the balance. Both are zero for a payment that is
	// recorded and still waiting to be settled.
	SettlementID int64 `json:"settlement_id"`
	SettlementNo int64 `json:"settlement_no"`

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
//
// For palay, Amount is ignored and worked out from Lines. For cash, Lines must
// be empty.
type Draft struct {
	CustomerID int64       `json:"customer_id"`
	Date       ledger.Date `json:"date"`
	Amount     money.Money `json:"amount"`
	Method     Method      `json:"method"`
	Lines      []LineDraft `json:"lines"`
	Particular string      `json:"particular"`
}

// UnmarshalJSON reads an empty amount as nothing typed rather than as a
// malformed one. A palay payment is sent with its amount blank, since the lines
// decide it, and a cash payment left blank is better refused by clean as a
// field to fix than as a request that could not be read. Anything else still
// goes through money's own reader, bare numbers and all.
//
// Unknown fields are refused here too: the API's decoder does that, and a
// custom reader would otherwise quietly accept a "status" or an "or_no".
func (d *Draft) UnmarshalJSON(data []byte) error {
	type plain Draft
	var raw struct {
		plain
		Amount json.RawMessage `json:"amount"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	*d = Draft(raw.plain)
	d.Amount = 0
	if len(raw.Amount) == 0 || string(raw.Amount) == `""` {
		return nil
	}
	return json.Unmarshal(raw.Amount, &d.Amount)
}

// cleaned is a draft that has been checked, with its lines costed.
type cleaned struct {
	Draft
	costed []Line
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
	ErrNotPosted = errors.New("only a recorded payment can be voided")

	// ErrSettled means a settlement has applied this payment to the balance.
	// Voiding it would leave that settlement describing money that never came
	// in, so the settlement is voided first — which reopens the payment.
	ErrSettled = errors.New("that payment has been settled: void the settlement first")
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
func (s *Service) clean(d Draft) (cleaned, error) {
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

	var costed []Line
	if out.Method == MethodPalay {
		lines, total, err := cleanLines(d.Lines)
		if err != nil {
			return cleaned{}, err
		}
		costed, out.Amount = lines, total
	} else if len(d.Lines) > 0 {
		return cleaned{}, ValidationError{
			Field: "lines", Message: "only a palay payment has lines — a cash payment is an amount"}
	}

	switch {
	case out.CustomerID <= 0:
		return cleaned{}, ValidationError{Field: "customer_id", Message: "a payment needs a customer"}
	case !out.Amount.IsPositive():
		// Money moving the other way is an advance or an adjustment, and both
		// are documents of their own.
		return cleaned{}, ValidationError{Field: "amount", Message: "a payment must be for more than nothing"}
	case out.Date.After(today):
		// Almost always a wrong year on a January receipt. Letting it through
		// puts money in the ledger that has not moved yet, which then turns up
		// on a report as a balance nobody can account for.
		return cleaned{}, ValidationError{
			Field:   "date",
			Message: fmt.Sprintf("a payment cannot be dated in the future — %s is after %s", out.Date, today),
		}
	case !out.Method.Valid():
		return cleaned{}, ValidationError{
			Field:   "method",
			Message: fmt.Sprintf("%q is not a way money arrives here — cash, palay or other", out.Method),
		}
	case len([]rune(out.Particular)) > maxParticularLen:
		return cleaned{}, ValidationError{
			Field:   "particular",
			Message: fmt.Sprintf("that particular is too long — %d characters at most", maxParticularLen),
		}
	}
	return cleaned{Draft: out, costed: costed}, nil
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
