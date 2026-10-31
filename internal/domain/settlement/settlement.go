// Package settlement owns the settlement: one farmer, settled up to a cut-off
// date.
//
// It is the legacy system's "Compute Percentage" screen and its Liquidate
// button, and it replaces the station-wide interest run that Step 7 built
// before the legacy program had been read. The legacy station never charged
// interest across every farmer at once. When a farmer delivered palay, the
// encoder opened that farmer, typed a cut-off date and a monthly rate, and the
// screen charged each open advance for the days it had been out, charged the
// principal carried from the last settlement for the days since then, and
// applied the deliveries — interest first. See docs/plans/07.5-settlement.md
// for the decision and docs/plans/legacy-findings.md for the evidence.
//
// The shape is the interest run's, because the reasons for it survived:
//
//   - **The draft is the preview.** It is computed by the same code that posts
//     it, so the figures somebody checks are the figures that are written.
//   - **Posting recomputes and refuses a stale preview.** A payment recorded
//     between the preview and the post changes the figures, and the post says
//     so rather than writing different ones.
//   - **Voiding undoes all of it**, and only a farmer's latest settlement can
//     be voided, so that no later settlement is left describing a carried
//     balance that no longer exists. Voiding reopens the advances and payments
//     it settled, which is what the legacy Recall screen's Delete did.
package settlement

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
	"github.com/Dannjann/bokkie-lms/internal/domain/payment"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// SourceTable is the provenance of the interest entry a settlement writes.
// The payment entries it writes are sourced to the payments themselves, so a
// statement still names each one by its OR No.
const SourceTable = "settlements"

// Status is where a settlement is in its life.
//
//	draft ──post──> posted ──void──> voided
type Status string

const (
	// StatusDraft is computed and not applied. It has no number, it has
	// written nothing, and it can be recomputed or deleted.
	StatusDraft Status = "draft"

	// StatusPosted is applied: its interest and its payments are in the
	// ledger.
	StatusPosted Status = "posted"

	// StatusVoided was posted and has been reversed, which reopened its
	// advances and payments.
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

// MaxRateBps is 100% a month. Nothing near it has ever been charged — the
// legacy data tops out at 8% — so a rate above it is a typing mistake.
const MaxRateBps = 10_000

// daysInMonth is the legacy system's month: rate × days × 12 ÷ 360.
const daysInMonth = 30

// Charge is the interest on one principal for a number of days at a monthly
// rate in basis points:
//
//	charge = principal × rate_bps × days ÷ (10,000 × 30), rounded half-up
//
// It is the legacy formula PRINCIPAL × PER × DAYS × (12/360), and it reproduces
// every one of the 7,072 liquidated advance rows in the legacy data to the
// centavo. Pure and exported so it can be checked against a calculator.
//
// A principal that is zero or negative — a farmer in credit — is charged
// nothing, and so are zero days and a zero rate.
func Charge(principal money.Money, rateBps int64, days int) (money.Money, error) {
	if !principal.IsPositive() || rateBps <= 0 || days <= 0 {
		return money.Zero, nil
	}
	charge, err := principal.MulDiv(rateBps*int64(days), 10_000*daysInMonth, money.RoundHalfUp)
	if err != nil {
		return money.Zero, fmt.Errorf("charge %d bps for %d days on %s: %w", rateBps, days, principal, err)
	}
	return charge, nil
}

// Settlement is one farmer, settled up to a cut-off date.
type Settlement struct {
	ID int64 `json:"id"`

	// No is zero until the settlement is posted.
	No int64 `json:"settlement_no"`

	CustomerID   int64  `json:"customer_id"`
	CustomerCode string `json:"customer_code"`
	CustomerName string `json:"customer_name"`

	Cutoff  ledger.Date `json:"cutoff"`
	RateBps int64       `json:"rate_bps"`
	Notes   string      `json:"notes"`
	Status  Status      `json:"status"`

	Carried Carried `json:"carried"`

	Advances []Advance `json:"advances"`
	Payments []Applied `json:"payments"`

	Totals Totals `json:"totals"`

	CreatedAt time.Time `json:"created_at"`
	CreatedBy Actor     `json:"created_by"`

	PostedAt *time.Time `json:"posted_at"`
	PostedBy Actor      `json:"posted_by"`

	VoidedAt   *time.Time `json:"voided_at"`
	VoidedBy   Actor      `json:"voided_by"`
	VoidReason string     `json:"void_reason"`
}

// Carried is what the farmer brought into this settlement from before: the
// legacy screen's "Previous Balance" box.
type Carried struct {
	// Principal is what was owed before the open advances — negative when an
	// earlier overpayment left the farmer in credit.
	Principal money.Money `json:"principal"`

	// Interest is unpaid interest from before. It is owed, and it is never
	// itself charged interest: the legacy system did not compound.
	Interest money.Money `json:"interest"`

	// Since is when Principal began accruing: the previous settlement's
	// cut-off, or the farmer's first entry if there has been none. Zero when
	// nothing was carried.
	Since ledger.Date `json:"since"`
	Days  int         `json:"days"`

	// Charge is the interest on Principal for Days.
	Charge money.Money `json:"charge"`
}

// Advance is one open advance the settlement charged interest on.
type Advance struct {
	AdvanceLineID int64       `json:"advance_line_id"`
	VoucherID     int64       `json:"voucher_id"` // the advance slip the line is on
	RefNo         int64       `json:"ref_no"`
	Date          ledger.Date `json:"date"`
	Particular    string      `json:"particular"`
	Principal     money.Money `json:"principal"`
	Days          int         `json:"days"`
	Charge        money.Money `json:"charge"`
}

// Applied is one recorded payment the settlement applied, and how it divided.
type Applied struct {
	PaymentID int64          `json:"payment_id"`
	OrNo      int64          `json:"or_no"`
	Date      ledger.Date    `json:"date"`
	Method    payment.Method `json:"method"`
	Amount    money.Money    `json:"amount"`
	Principal money.Money    `json:"principal"`
	Interest  money.Money    `json:"interest"`
}

// Totals is the arithmetic of the settlement, added up here rather than in
// the browser: the legacy screen's TOTALS, Less: Payment, and Present Balance.
type Totals struct {
	Advanced        money.Money `json:"advanced"`         // the open advances
	AdvanceInterest money.Money `json:"advance_interest"` // interest on them
	InterestCharged money.Money `json:"interest_charged"` // that plus interest on carried principal

	Principal money.Money `json:"principal"` // carried principal + advanced
	Interest  money.Money `json:"interest"`  // carried interest + interest charged
	Owed      money.Money `json:"owed"`

	Paid          money.Money `json:"paid"`
	PaidPrincipal money.Money `json:"paid_principal"`
	PaidInterest  money.Money `json:"paid_interest"`

	PrincipalAfter money.Money `json:"principal_after"`
	InterestAfter  money.Money `json:"interest_after"`
	BalanceAfter   money.Money `json:"balance_after"`
}

// Draft is everything a caller decides: who, up to when, at what rate.
//
// A nil RateBps takes the station's default. Zero is a real rate — the legacy
// data has settlements at 0%, which is how a charge was waived — so "not
// given" cannot be spelled as zero.
type Draft struct {
	CustomerID int64       `json:"customer_id"`
	Cutoff     ledger.Date `json:"cutoff"`
	RateBps    *int64      `json:"rate_bps"`
	Notes      string      `json:"notes"`
}

// Actor is who did something — the ledger's, aliased, as in the other
// documents.
type Actor = ledger.Actor

// ValidationError is a complaint about what somebody chose.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string { return e.Message }

var (
	// ErrNotFound means there is no settlement with that id.
	ErrNotFound = errors.New("no such liquidation")

	// ErrNotDraft means it has been posted already.
	ErrNotDraft = errors.New("that liquidation has been posted already")

	// ErrNotPosted means only a posted settlement can be voided.
	ErrNotPosted = errors.New("only a posted liquidation can be voided")

	// ErrNotLatest means a later settlement for the same farmer is posted.
	// That one carried forward the balance this one left; void it first.
	ErrNotLatest = errors.New("a later liquidation for this farmer is posted: void that one first")

	// ErrPreviewStale means the record moved between the preview and the post.
	ErrPreviewStale = errors.New("the record has moved since this liquidation was computed")

	// ErrNoPayment means there is nothing to apply. The legacy Liquidate
	// button stayed disabled until a payment was on the screen, and so does
	// this: a settlement is what happens when a farmer pays.
	ErrNoPayment = errors.New("there is no recorded payment to liquidate")
)

const maxNotesLen = 1000

// Station is what this package needs to know about where it is running.
// settings.Store satisfies it.
type Station interface {
	Location() *time.Location
	Int(d settings.Def) int
	Choice(d settings.Def) string
}

// Service is the settlement.
type Service struct {
	db       *sql.DB
	ledger   *ledger.Service
	payments *payment.Service
	station  Station
	log      *slog.Logger

	now func() time.Time
}

// New returns a settlement service. It needs the ledger, which it reads to
// compute and writes to post, and the payments, whose recorded deliveries it
// applies.
func New(database *sql.DB, entries *ledger.Service, payments *payment.Service, station Station, log *slog.Logger) *Service {
	return &Service{db: database, ledger: entries, payments: payments, station: station, log: log, now: time.Now}
}

func (s *Service) today() ledger.Date {
	return ledger.DateOf(s.now().In(s.station.Location()))
}

// DefaultRateBps is the station's usual monthly rate.
func (s *Service) DefaultRateBps() int64 {
	return int64(s.station.Int(settings.InterestRateBps))
}

type queryExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// cleanDraft is a checked draft with the rate resolved.
type cleanDraft struct {
	CustomerID int64
	Cutoff     ledger.Date
	RateBps    int64
	Notes      string
}

func (s *Service) clean(d Draft) (cleanDraft, error) {
	today := s.today()
	out := cleanDraft{CustomerID: d.CustomerID, Cutoff: d.Cutoff, Notes: strings.TrimSpace(d.Notes)}
	if out.Cutoff.IsZero() {
		out.Cutoff = today
	}
	if d.RateBps != nil {
		out.RateBps = *d.RateBps
	} else {
		out.RateBps = s.DefaultRateBps()
	}

	switch {
	case out.CustomerID <= 0:
		return cleanDraft{}, ValidationError{Field: "customer_id", Message: "a liquidation needs a farmer"}
	case out.Cutoff.After(today):
		return cleanDraft{}, ValidationError{
			Field:   "cutoff",
			Message: fmt.Sprintf("a cut-off cannot be in the future — %s is after %s", out.Cutoff, today),
		}
	case out.RateBps < 0:
		return cleanDraft{}, ValidationError{Field: "rate_bps", Message: "a rate cannot be negative"}
	case out.RateBps > MaxRateBps:
		return cleanDraft{}, ValidationError{
			Field: "rate_bps", Message: "that rate is more than 100% a month — check the decimal point"}
	case len([]rune(out.Notes)) > maxNotesLen:
		return cleanDraft{}, ValidationError{
			Field: "notes", Message: fmt.Sprintf("those notes are too long — %d characters at most", maxNotesLen)}
	}
	return out, nil
}

// refused turns "the statement matched no rows" into the sentence that says
// why. It reads after the failed write, so it cannot be a lock upgrade.
func (s *Service) refused(ctx context.Context, q queryExecer, id int64, res sql.Result, want Status) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("settlement %d: %w", id, err)
	}
	if affected > 0 {
		return nil
	}

	var status string
	switch err := q.QueryRowContext(ctx, `SELECT status FROM settlements WHERE id = ?`, id).Scan(&status); {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: %d", ErrNotFound, id)
	case err != nil:
		return fmt.Errorf("read the status of settlement %d: %w", id, err)
	}

	if want == StatusPosted {
		if Status(status) == StatusPosted {
			return fmt.Errorf("%w: liquidation %d", ErrNotLatest, id)
		}
		return fmt.Errorf("%w: liquidation %d is %s", ErrNotPosted, id, status)
	}
	return fmt.Errorf("%w: liquidation %d is %s", ErrNotDraft, id, status)
}

func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func nullableDate(d ledger.Date) any {
	if d.IsZero() {
		return nil
	}
	return d.String()
}
