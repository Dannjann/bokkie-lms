package payment

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
// depends on the tz database or on where the machine running it is, an OR No.
// sequence that starts wherever a test needs it to, and an application order a
// test can swap.
type station struct {
	orNoStart int
	applies   Rule
}

func (station) Location() *time.Location { return time.FixedZone("PHT", 8*60*60) }

func (s station) Int(d settings.Def) int {
	if d.Key == settings.OrNoStart.Key && s.orNoStart > 0 {
		return s.orNoStart
	}
	n, _ := strconv.Atoi(d.Default)
	return n
}

func (s station) Choice(d settings.Def) string {
	if d.Key == settings.PaymentApplies.Key && s.applies != "" {
		return string(s.applies)
	}
	return d.Default
}

// today is the day the test clock is frozen on. Dates in tests are written
// relative to it, because "not in the future" is one of the rules under test.
var today = ledger.MustDate(2026, time.March, 2)

var (
	dan     = Actor{UserID: 1, Username: "dan"}
	manager = Actor{UserID: 2, Username: "manager"}
)

func openTestService(t *testing.T) (*Service, *ledger.Service, *sql.DB) {
	t.Helper()
	return openStationService(t, station{})
}

// openStationService is openTestService with the station's settings chosen by
// the test: where the OR No. sequence starts, and which half of a debt a
// payment clears first.
func openStationService(t *testing.T, st station) (*Service, *ledger.Service, *sql.DB) {
	t.Helper()
	return reopen(t, openSchema(t), st)
}

// reopen builds a second service over a database that already has something in
// it, which is how a test asks what a *changed* setting does to a sequence that
// has already begun.
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
			 VALUES (?, ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
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
			 VALUES (?, ?, ?, 'x', ?, '2026-01-01T00:00:00Z')`, u.id, u.name, u.name, u.role); err != nil {
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

// draftOf is a payment from one customer for a number of pesos. Most tests care
// about what happens to a payment rather than about what is on it.
func draftOf(t *testing.T, customerID, amount int64) Draft {
	t.Helper()
	return Draft{
		CustomerID: customerID,
		Date:       today,
		Amount:     pesos(t, amount),
		Method:     MethodCash,
		Particular: "partial payment",
	}
}

// inTx runs fn in a transaction and commits it, which is what the API's route
// wrapper does around every one of these methods.
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

func mustCreate(t *testing.T, svc *Service, database *sql.DB, d Draft) Payment {
	t.Helper()

	var p Payment
	err := inTx(t, database, func(tx *sql.Tx) error {
		var err error
		p, err = svc.CreateTx(context.Background(), tx, d, dan)
		return err
	})
	if err != nil {
		t.Fatalf("create a draft: %v", err)
	}
	return p
}

func mustPost(t *testing.T, svc *Service, database *sql.DB, id int64, by Actor) Payment {
	t.Helper()

	var p Payment
	err := inTx(t, database, func(tx *sql.Tx) error {
		var err error
		p, err = svc.PostTx(context.Background(), tx, id, by)
		return err
	})
	if err != nil {
		t.Fatalf("post payment %d: %v", id, err)
	}
	return p
}

// owe puts a debt on a customer directly through the ledger, so that a test
// about payments does not have to build a voucher to have something to pay off.
// Interest is written the way step 7's run will write it.
func owe(t *testing.T, entries *ledger.Service, customerID int64, principal, interest money.Money) {
	t.Helper()
	ctx := context.Background()

	if principal.IsPositive() {
		if _, err := entries.Post(ctx, ledger.Entry{
			CustomerID: customerID, Date: today.AddDays(-7), Type: ledger.TypeAdvance,
			Principal: principal, Interest: money.Zero, By: dan,
		}); err != nil {
			t.Fatalf("lend customer %d %s: %v", customerID, principal, err)
		}
	}
	if interest.IsPositive() {
		if _, err := entries.Post(ctx, ledger.Entry{
			CustomerID: customerID, Date: today.AddDays(-1), Type: ledger.TypeInterest,
			Principal: money.Zero, Interest: interest, By: dan,
		}); err != nil {
			t.Fatalf("charge customer %d %s: %v", customerID, interest, err)
		}
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

func TestADraftIsSavedWithNoNumberAndNoMoneyMoved(t *testing.T) {
	svc, entries, database := openTestService(t)

	owe(t, entries, 1, pesos(t, 5000), money.Zero)
	p := mustCreate(t, svc, database, draftOf(t, 1, 2000))

	if p.Status != StatusDraft {
		t.Errorf("a new payment is %s, want draft", p.Status)
	}
	if p.OrNo != 0 {
		t.Errorf("a draft was given OR No. %d — numbering happens at posting", p.OrNo)
	}
	if p.PostedAt != nil {
		t.Errorf("a draft carries a posted_at of %v", p.PostedAt)
	}
	if p.CustomerCode != "BA-0001" {
		t.Errorf("the draft is for %q, want BA-0001", p.CustomerCode)
	}

	// The whole point of a draft: it has moved nothing.
	if got := balanceOf(t, entries, 1).Principal; got.Centavos() != 500000 {
		t.Errorf("customer 1 owes %s after a draft, want the 5,000 they were lent", got)
	}
}

func TestADraftIsEditedAndDeleted(t *testing.T) {
	svc, _, database := openTestService(t)
	ctx := context.Background()

	p := mustCreate(t, svc, database, draftOf(t, 1, 2000))

	// Switching to palay: the amount comes from the lines, 250 kg at ₱14.00.
	edited := draftOf(t, 2, 1)
	edited.Method = MethodPalay
	edited.Particular = "netted against delivery"
	edited.Lines = []LineDraft{{Class: "slg", Sacks: 5, Kilos: KilosFromTenths(2500), Price: pesos(t, 14)}}

	var after Payment
	if err := inTx(t, database, func(tx *sql.Tx) error {
		var err error
		after, err = svc.UpdateTx(ctx, tx, p.ID, edited)
		return err
	}); err != nil {
		t.Fatalf("edit a draft: %v", err)
	}

	if after.CustomerID != 2 || after.Amount.Centavos() != 350000 || after.Method != MethodPalay {
		t.Errorf("editing left customer %d, %s, %s", after.CustomerID, after.Amount, after.Method)
	}
	if len(after.Lines) != 1 || after.Lines[0].Class != "SLG" || after.Lines[0].Cost.Centavos() != 350000 {
		t.Errorf("the palay lines came back as %+v", after.Lines)
	}

	if err := inTx(t, database, func(tx *sql.Tx) error {
		return svc.DeleteTx(ctx, tx, p.ID)
	}); err != nil {
		t.Fatalf("delete a draft: %v", err)
	}
	if _, err := svc.ByID(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading a deleted draft gave %v, want ErrNotFound", err)
	}
}

// Deactivation says who the station lends to today. It says nothing about who
// owes money from last season — and refusing that farmer's payment would leave
// a debt with no way to clear it but an adjustment.
func TestADeactivatedCustomerCanStillPay(t *testing.T) {
	svc, entries, database := openTestService(t)

	owe(t, entries, 1, pesos(t, 5000), money.Zero)
	if _, err := database.Exec(`UPDATE customers SET active = 0 WHERE id = 1`); err != nil {
		t.Fatalf("deactivate customer 1: %v", err)
	}

	p := mustCreate(t, svc, database, draftOf(t, 1, 5000))
	if recorded := mustPost(t, svc, database, p.ID, dan); recorded.Status != StatusPosted {
		t.Errorf("a deactivated customer's payment is %s, want recorded", recorded.Status)
	}
	_ = entries
}

func TestAPaymentForSomebodyNotOnTheBooksIsRefused(t *testing.T) {
	svc, _, database := openTestService(t)

	err := inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.CreateTx(context.Background(), tx, draftOf(t, 99, 1000), dan)
		return err
	})

	var invalid ValidationError
	if !errors.As(err, &invalid) || invalid.Field != "customer_id" {
		t.Fatalf("creating a payment for a stranger gave %v, want a complaint about customer_id", err)
	}
}

func TestWhatADraftMustSay(t *testing.T) {
	svc, _, database := openTestService(t)
	ctx := context.Background()

	bad := []struct {
		name  string
		draft Draft
		field string
	}{
		{"no customer", Draft{Date: today, Amount: pesos(t, 100)}, "customer_id"},
		{"nothing at all", Draft{CustomerID: 1, Date: today, Amount: money.Zero}, "amount"},
		{"a negative amount", Draft{CustomerID: 1, Date: today, Amount: pesos(t, -100)}, "amount"},
		{"dated tomorrow", Draft{CustomerID: 1, Date: today.AddDays(1), Amount: pesos(t, 100)}, "date"},
		{"an invented method", Draft{CustomerID: 1, Date: today, Amount: pesos(t, 100), Method: "cheque"}, "method"},
	}

	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			err := inTx(t, database, func(tx *sql.Tx) error {
				_, err := svc.CreateTx(ctx, tx, tc.draft, dan)
				return err
			})
			var invalid ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %v, want a validation error", err)
			}
			if invalid.Field != tc.field {
				t.Errorf("the complaint is about %q, want %q", invalid.Field, tc.field)
			}
		})
	}
}

// A payment with no date of its own is today's. A receipt is written as the
// money is handed over, so the default is the useful one rather than an error.
func TestADraftWithNoDateIsDatedToday(t *testing.T) {
	svc, _, database := openTestService(t)

	d := draftOf(t, 1, 1000)
	d.Date = ledger.Date{}
	p := mustCreate(t, svc, database, d)

	if !p.Date.Equal(today) {
		t.Errorf("a payment with no date is dated %s, want today %s", p.Date, today)
	}
}

// Cost is kilos × price, rounded half-up, which is what the legacy screen did
// and what a person does with a calculator.
func TestTheCostOfALineOfPalay(t *testing.T) {
	cases := []struct {
		kilos string
		price int64 // centavos per kilo
		want  int64 // centavos
	}{
		{"500", 1400, 700000},
		{"1234.5", 1450, 1790025},
		{"0.1", 1005, 101}, // 100.5 centavos rounds up
		{"1,000.0", 1570, 1570000},
	}
	for _, tc := range cases {
		k, err := ParseKilos(tc.kilos)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.kilos, err)
		}
		got, err := Cost(k, money.FromCentavos(tc.price))
		if err != nil {
			t.Fatal(err)
		}
		if got.Centavos() != tc.want {
			t.Errorf("%s kg at %s is %s, want %s", tc.kilos, money.FromCentavos(tc.price), got, money.FromCentavos(tc.want))
		}
	}

	for _, bad := range []string{"", "12.34", "-5", "abc", "+3"} {
		if _, err := ParseKilos(bad); err == nil {
			t.Errorf("ParseKilos(%q) was accepted", bad)
		}
	}
}
