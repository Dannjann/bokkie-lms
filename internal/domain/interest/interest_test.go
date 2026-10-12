package interest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	bokkiedb "github.com/Dannjann/bokkie-lms/internal/db"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// station is the station in tests: a clock fixed at +08:00, so nothing here
// depends on the tz database or on where the machine running it is, and an
// interest rule a test can set to whatever it is about.
type station struct {
	method  Method
	rateBps int
	basis   Basis
}

// charging is the station most tests want: three percent, on the balance, on
// principal alone.
func charging() station {
	return station{method: MethodMonthlyOnBalance, rateBps: 300, basis: BasisPrincipal}
}

func (station) Location() *time.Location { return time.FixedZone("PHT", 8*60*60) }

func (s station) Int(d settings.Def) int {
	if d.Key == settings.InterestRateBps.Key && s.rateBps != 0 {
		return s.rateBps
	}
	n, _ := strconv.Atoi(d.Default)
	return n
}

func (s station) Choice(d settings.Def) string {
	switch {
	case d.Key == settings.InterestMethod.Key && s.method != "":
		return string(s.method)
	case d.Key == settings.InterestBasis.Key && s.basis != "":
		return string(s.basis)
	}
	return d.Default
}

// today is the day the test clock is frozen on. A period may not end after it.
var today = ledger.MustDate(2026, time.March, 2)

// september is the period most tests run over: entirely in the past, a month
// long, and far enough from today that a reversal dated today falls outside it.
var (
	septemberFrom = ledger.MustDate(2025, time.September, 1)
	septemberTo   = ledger.MustDate(2025, time.September, 30)
)

var (
	dan     = Actor{UserID: 1, Username: "dan"}
	manager = Actor{UserID: 2, Username: "manager"}
)

func openTestService(t *testing.T) (*Service, *ledger.Service, *sql.DB) {
	t.Helper()
	return openStationService(t, charging())
}

// openStationService is openTestService with the rule chosen by the test.
func openStationService(t *testing.T, st station) (*Service, *ledger.Service, *sql.DB) {
	t.Helper()
	return reopen(t, openSchema(t), st)
}

// reopen builds a second service over a database that already has something in
// it, which is how a test asks what a *changed* rule does to a run that was
// computed under the old one.
func reopen(t *testing.T, database *sql.DB, st station) (*Service, *ledger.Service, *sql.DB) {
	t.Helper()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	entries := ledger.New(database, st, audit.New(database, log), log)

	svc := New(database, entries, st, log)
	svc.now = func() time.Time { return time.Date(2026, 3, 2, 9, 30, 0, 0, st.Location()) }
	return svc, entries, database
}

func openSchema(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	database, err := bokkiedb.Open(ctx, filepath.Join(t.TempDir(), "bokkie.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := bokkiedb.Migrate(ctx, database, log); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for id := int64(1); id <= 3; id++ {
		if _, err := database.Exec(
			`INSERT INTO customers (id, code, name, created_at, updated_at)
			 VALUES (?, ?, ?, '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`,
			id, fmt.Sprintf("BA-%04d", id), fmt.Sprintf("Customer %d", id)); err != nil {
			t.Fatalf("add customer %d: %v", id, err)
		}
	}
	for _, u := range []struct {
		id   int64
		name string
		role string
	}{{1, "dan", "encoder"}, {2, "manager", "manager"}} {
		if _, err := database.Exec(
			`INSERT INTO users (id, username, display_name, password_hash, role, created_at)
			 VALUES (?, ?, ?, 'x', ?, '2025-01-01T00:00:00Z')`, u.id, u.name, u.name, u.role); err != nil {
			t.Fatalf("add user %s: %v", u.name, err)
		}
	}
	return database
}

// pesos is the amount a test means, in the type the system uses.
func pesos(t *testing.T, amount int64) money.Money {
	t.Helper()
	m, err := money.FromPesos(amount)
	if err != nil {
		t.Fatalf("build an amount: %v", err)
	}
	return m
}

// september is a draft over the month every test uses.
func september() Draft {
	return Draft{From: septemberFrom, To: septemberTo, Notes: "September interest"}
}

func inTx(t *testing.T, database *sql.DB, fn func(tx *sql.Tx) error) error {
	t.Helper()

	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return nil
}

func create(t *testing.T, svc *Service, database *sql.DB, d Draft) (Run, error) {
	t.Helper()

	var run Run
	err := inTx(t, database, func(tx *sql.Tx) error {
		var err error
		run, err = svc.CreateTx(context.Background(), tx, d, dan)
		return err
	})
	return run, err
}

func mustCreate(t *testing.T, svc *Service, database *sql.DB, d Draft) Run {
	t.Helper()

	run, err := create(t, svc, database, d)
	if err != nil {
		t.Fatalf("compute a run: %v", err)
	}
	return run
}

func post(t *testing.T, svc *Service, database *sql.DB, id int64, by Actor) (Run, error) {
	t.Helper()

	var run Run
	err := inTx(t, database, func(tx *sql.Tx) error {
		var err error
		run, err = svc.PostTx(context.Background(), tx, id, by)
		return err
	})
	return run, err
}

func mustPost(t *testing.T, svc *Service, database *sql.DB, id int64, by Actor) Run {
	t.Helper()

	run, err := post(t, svc, database, id, by)
	if err != nil {
		t.Fatalf("post interest run %d: %v", id, err)
	}
	return run
}

func mustVoid(t *testing.T, svc *Service, database *sql.DB, id int64, reason string) Run {
	t.Helper()

	var run Run
	err := inTx(t, database, func(tx *sql.Tx) error {
		var err error
		run, err = svc.VoidTx(context.Background(), tx, id, manager, reason)
		return err
	})
	if err != nil {
		t.Fatalf("void interest run %d: %v", id, err)
	}
	return run
}

// lend puts a debt on a customer through the ledger directly, dated inside the
// September period, so a test about interest does not have to build a voucher
// to have something to charge on.
func lend(t *testing.T, entries *ledger.Service, customerID int64, amount money.Money) int64 {
	t.Helper()

	id, err := entries.Post(context.Background(), ledger.Entry{
		CustomerID: customerID, Date: septemberFrom.AddDays(4), Type: ledger.TypeAdvance,
		Principal: amount, Interest: money.Zero, By: dan,
	})
	if err != nil {
		t.Fatalf("lend customer %d %s: %v", customerID, amount, err)
	}
	return id
}

func repay(t *testing.T, entries *ledger.Service, customerID int64, on ledger.Date, principal money.Money) {
	t.Helper()

	amount, err := principal.Neg()
	if err != nil {
		t.Fatalf("negate %s: %v", principal, err)
	}
	if _, err := entries.Post(context.Background(), ledger.Entry{
		CustomerID: customerID, Date: on, Type: ledger.TypePayment,
		Principal: amount, Interest: money.Zero, By: dan,
	}); err != nil {
		t.Fatalf("repay customer %d: %v", customerID, err)
	}
}

func balanceOf(t *testing.T, entries *ledger.Service, customerID int64) ledger.Balance {
	t.Helper()

	b, err := entries.BalanceFor(context.Background(), customerID)
	if err != nil {
		t.Fatalf("read the balance of customer %d: %v", customerID, err)
	}
	return b
}

func TestADraftIsThePreviewAndChargesNothingYet(t *testing.T) {
	svc, entries, database := openTestService(t)

	lend(t, entries, 1, pesos(t, 5000))
	lend(t, entries, 2, pesos(t, 1000))

	run := mustCreate(t, svc, database, september())

	if run.RunNo != 0 {
		t.Errorf("a draft run is numbered %d; it should have no number until it is posted", run.RunNo)
	}
	if run.Status != StatusDraft {
		t.Errorf("a new run is %s, want draft", run.Status)
	}
	if len(run.Lines) != 2 {
		t.Fatalf("the run has %d lines, want one per borrower: %+v", len(run.Lines), run.Lines)
	}

	// 3% of 5,000 and of 1,000.
	if got := run.Lines[0].Charge.String(); got != "150.00" {
		t.Errorf("BA-0001 is charged %s, want 150.00", got)
	}
	if got := run.Lines[1].Charge.String(); got != "30.00" {
		t.Errorf("BA-0002 is charged %s, want 30.00", got)
	}
	if got := run.Total.String(); got != "180.00" {
		t.Errorf("the run totals %s, want 180.00", got)
	}

	// And the balances have not moved, because a preview is not a charge.
	if got := balanceOf(t, entries, 1).Interest; !got.IsZero() {
		t.Errorf("BA-0001 owes %s of interest after a preview; a draft charges nothing", got)
	}
}

func TestARunLeavesOutWhoeverOwesNothing(t *testing.T) {
	svc, entries, database := openTestService(t)

	lend(t, entries, 1, pesos(t, 5000))
	repay(t, entries, 1, septemberTo, pesos(t, 5000))
	lend(t, entries, 2, pesos(t, 1000))
	// Customer 3 has never borrowed at all.

	run := mustCreate(t, svc, database, september())

	if len(run.Lines) != 1 || run.Lines[0].CustomerID != 2 {
		t.Fatalf("the run charges %+v; only BA-0002 still owes anything", run.Lines)
	}
}

// A farmer in credit is not charged interest on the credit. The clamp is in
// Charge as well as here, for the reason payment.Split clamps rather than
// trusting its caller.
func TestACustomerInCreditIsNotCharged(t *testing.T) {
	svc, entries, database := openTestService(t)

	lend(t, entries, 1, pesos(t, 1000))
	repay(t, entries, 1, septemberTo, pesos(t, 1500))

	run := mustCreate(t, svc, database, september())

	if len(run.Lines) != 0 {
		t.Fatalf("a customer in credit was charged %+v", run.Lines)
	}
}

// Nothing charges interest until somebody sets a rule, and the refusal is a
// sentence a manager can act on rather than a run full of zeroes.
func TestNoRuleMeansNoRun(t *testing.T) {
	svc, entries, database := openStationService(t, station{})

	lend(t, entries, 1, pesos(t, 5000))

	_, err := create(t, svc, database, september())
	var invalid ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("creating a run with no rule set: got %v, want a validation error", err)
	}
	if invalid.Field != settings.InterestMethod.Key {
		t.Errorf("the complaint names %q; it should name the setting to change", invalid.Field)
	}
}

func TestARateOfZeroMeansNoRun(t *testing.T) {
	svc, entries, database := openStationService(t,
		station{method: MethodMonthlyOnBalance, basis: BasisPrincipal})

	lend(t, entries, 1, pesos(t, 5000))

	_, err := create(t, svc, database, september())
	var invalid ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("creating a run at a rate of zero: got %v, want a validation error", err)
	}
	if invalid.Field != settings.InterestRateBps.Key {
		t.Errorf("the complaint names %q; it should name the rate", invalid.Field)
	}
}

func TestAPeriodMustRunForwardsAndEndInThePast(t *testing.T) {
	svc, _, database := openTestService(t)

	cases := []struct {
		name  string
		draft Draft
	}{
		{"backwards", Draft{From: septemberTo, To: septemberFrom}},
		{"ending in the future", Draft{From: today, To: today.AddDays(1)}},
		{"no period at all", Draft{}},
		{"only one end", Draft{From: septemberFrom}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := create(t, svc, database, tc.draft); err == nil {
				t.Fatal("got no error, want one")
			}
		})
	}
}

// The rule is copied onto the run, so changing the setting does not change a
// draft that already exists. Recomputing it is what picks the new rate up —
// which is also how a manager corrects a run made under the wrong one.
func TestTheRuleIsSnapshottedAndRecomputingPicksUpANewOne(t *testing.T) {
	svc, entries, database := openTestService(t)
	lend(t, entries, 1, pesos(t, 5000))

	run := mustCreate(t, svc, database, september())
	if run.Rule.RateBps != 300 {
		t.Fatalf("the run was computed at %d bps, want 300", run.Rule.RateBps)
	}

	// The station's rate changes. The draft does not.
	dearer, _, _ := reopen(t, database, station{method: MethodMonthlyOnBalance, rateBps: 500, basis: BasisPrincipal})

	unchanged, err := dearer.ByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("read the run back: %v", err)
	}
	if unchanged.Rule.RateBps != 300 || unchanged.Lines[0].Charge.String() != "150.00" {
		t.Errorf("the draft changed under a new setting: %d bps, %s",
			unchanged.Rule.RateBps, unchanged.Lines[0].Charge)
	}

	var recomputed Run
	if err := inTx(t, database, func(tx *sql.Tx) error {
		var err error
		recomputed, err = dearer.UpdateTx(context.Background(), tx, run.ID, september())
		return err
	}); err != nil {
		t.Fatalf("recompute the run: %v", err)
	}
	if recomputed.Rule.RateBps != 500 || recomputed.Lines[0].Charge.String() != "250.00" {
		t.Errorf("recomputing gave %d bps and %s, want 500 and 250.00",
			recomputed.Rule.RateBps, recomputed.Lines[0].Charge)
	}
}

func TestCompoundingChargesOnUnpaidInterestToo(t *testing.T) {
	svc, entries, database := openStationService(t,
		station{method: MethodMonthlyOnBalance, rateBps: 1000, basis: BasisPrincipalAndInterest})

	lend(t, entries, 1, pesos(t, 1000))
	if _, err := entries.Post(context.Background(), ledger.Entry{
		CustomerID: 1, Date: septemberFrom.AddDays(5), Type: ledger.TypeInterest,
		Principal: money.Zero, Interest: pesos(t, 100), By: dan,
	}); err != nil {
		t.Fatalf("charge interest by hand: %v", err)
	}

	run := mustCreate(t, svc, database, september())

	// 10% of 1,100 rather than of 1,000 — which is the whole of the difference
	// between simple and compound interest, and the reason the basis is a
	// setting somebody has to choose rather than a default nobody notices.
	if got := run.Lines[0].Charge.String(); got != "110.00" {
		t.Errorf("compounding charged %s, want 110.00", got)
	}
	if got := run.Lines[0].Base.String(); got != "1100.00" {
		t.Errorf("the base is %s, want 1100.00", got)
	}
}

func TestFlatOnAdvanceChargesWhatWasLentInThePeriod(t *testing.T) {
	svc, entries, database := openStationService(t,
		station{method: MethodFlatOnAdvance, rateBps: 300, basis: BasisPrincipal})

	lend(t, entries, 1, pesos(t, 5000))
	// Repaid in full inside the period. A flat fee is charged on what was lent
	// regardless — that is what makes it a different method rather than a
	// different rate.
	repay(t, entries, 1, septemberTo, pesos(t, 5000))

	run := mustCreate(t, svc, database, september())

	if len(run.Lines) != 1 || run.Lines[0].Charge.String() != "150.00" {
		t.Fatalf("a flat charge on 5,000 lent gave %+v, want 150.00", run.Lines)
	}
}

func TestADraftCanBeDeletedAndIsGone(t *testing.T) {
	svc, entries, database := openTestService(t)
	lend(t, entries, 1, pesos(t, 5000))

	run := mustCreate(t, svc, database, september())
	if err := inTx(t, database, func(tx *sql.Tx) error {
		return svc.DeleteTx(context.Background(), tx, run.ID)
	}); err != nil {
		t.Fatalf("delete the draft: %v", err)
	}

	if _, err := svc.ByID(context.Background(), run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reading a deleted draft: got %v, want ErrNotFound", err)
	}

	var lines int
	if err := database.QueryRow(`SELECT COUNT(*) FROM interest_run_lines`).Scan(&lines); err != nil {
		t.Fatalf("count the lines: %v", err)
	}
	if lines != 0 {
		t.Errorf("%d lines survived the draft that owned them", lines)
	}
}
