package report

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	bokkiedb "github.com/Dannjann/bokkie-lms/internal/db"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

type station struct{}

func (station) Location() *time.Location { return time.FixedZone("PHT", 8*60*60) }

var (
	dan     = ledger.Actor{UserID: 1, Username: "dan"}
	manager = ledger.Actor{UserID: 2, Username: "manager"}
)

func openTestService(t *testing.T) (*Service, *ledger.Service, *sql.DB) {
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
			t.Fatalf("add customer: %v", err)
		}
	}
	for _, u := range []struct {
		id   int64
		name string
	}{{1, "dan"}, {2, "manager"}} {
		if _, err := database.Exec(
			`INSERT INTO users (id, username, display_name, password_hash, role, created_at)
			 VALUES (?, ?, ?, 'x', 'manager', '2025-01-01T00:00:00Z')`, u.id, u.name, u.name); err != nil {
			t.Fatalf("add user: %v", err)
		}
	}

	entries := ledger.New(database, station{}, audit.New(database, log), log)
	return New(database, entries, log), entries, database
}

func lend(t *testing.T, entries *ledger.Service, customer int64, on ledger.Date, pesos int64) int64 {
	t.Helper()
	id, err := entries.Post(context.Background(), ledger.Entry{
		CustomerID: customer, Date: on, Type: ledger.TypeAdvance,
		Principal: mustPesos(pesos), By: dan,
	})
	if err != nil {
		t.Fatalf("lend: %v", err)
	}
	return id
}

func repay(t *testing.T, entries *ledger.Service, customer int64, on ledger.Date, principal, interest int64) {
	t.Helper()
	p, err := mustPesos(principal).Neg()
	if err != nil {
		t.Fatal(err)
	}
	i, err := mustPesos(interest).Neg()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entries.Post(context.Background(), ledger.Entry{
		CustomerID: customer, Date: on, Type: ledger.TypePayment,
		Principal: p, Interest: i, By: dan,
	}); err != nil {
		t.Fatalf("repay: %v", err)
	}
}

func charge(t *testing.T, entries *ledger.Service, customer int64, on ledger.Date, pesos int64) {
	t.Helper()
	if _, err := entries.Post(context.Background(), ledger.Entry{
		CustomerID: customer, Date: on, Type: ledger.TypeInterest,
		Interest: mustPesos(pesos), By: manager,
	}); err != nil {
		t.Fatalf("charge interest: %v", err)
	}
}

// settle puts a posted settlement on the books directly, as the settlement
// package would leave it: a payment applied, split between principal and
// interest, and the matching ledger entry dated the cut-off. voidedOn, when not
// zero, voids it on that day.
func settle(t *testing.T, db *sql.DB, entries *ledger.Service, customer int64, cutoff ledger.Date,
	principal, interest int64, voidedOn ledger.Date) int64 {
	t.Helper()
	ctx := context.Background()
	amount := mustPesos(principal + interest)

	res, err := db.Exec(`INSERT INTO payments (customer_id, payment_date, amount_cents, method, status,
	                          created_at, created_by_username)
	                     VALUES (?, ?, ?, 'cash', 'draft', '2026-01-01T00:00:00Z', 'dan')`,
		customer, cutoff, amount)
	if err != nil {
		t.Fatalf("record a payment: %v", err)
	}
	paymentID, _ := res.LastInsertId()
	if _, err := db.Exec(`UPDATE payments SET status = 'posted', or_no = ?, posted_at = '2026-01-01T00:00:00Z',
	                          posted_by_username = 'dan' WHERE id = ?`, paymentID, paymentID); err != nil {
		t.Fatalf("record a payment: %v", err)
	}

	res, err = db.Exec(`INSERT INTO settlements (customer_id, cutoff_date, rate_bps, status, created_at, created_by_username)
	                    VALUES (?, ?, 500, 'draft', '2026-01-01T00:00:00Z', 'dan')`, customer, cutoff)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	id, _ := res.LastInsertId()
	if _, err := db.Exec(`INSERT INTO settlement_payments (settlement_id, payment_id, principal_cents, interest_cents)
	                      VALUES (?, ?, ?, ?)`, id, paymentID, mustPesos(principal), mustPesos(interest)); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if _, err := db.Exec(`UPDATE settlements SET status = 'posted', settlement_no = ?, posted_at = '2026-01-01T00:00:00Z',
	                          posted_by_username = 'dan' WHERE id = ?`, id, id); err != nil {
		t.Fatalf("settle: %v", err)
	}

	p, _ := mustPesos(principal).Neg()
	i, _ := mustPesos(interest).Neg()
	entryID, err := entries.Post(ctx, ledger.Entry{
		CustomerID: customer, Date: cutoff, Type: ledger.TypePayment, Principal: p, Interest: i,
		SourceTable: "payments", SourceID: paymentID, By: dan,
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}

	if !voidedOn.IsZero() {
		if _, err := db.Exec(`UPDATE settlements SET status = 'voided', voided_at = ?, voided_by_username = 'manager',
		                          void_reason = 'wrong rate' WHERE id = ?`, voidedOn.String()+"T02:00:00Z", id); err != nil {
			t.Fatalf("void: %v", err)
		}
		if _, err := entries.Reverse(ctx, entryID, manager, "wrong rate"); err != nil {
			t.Fatalf("void: %v", err)
		}
	}
	return id
}

var (
	january = ledger.MustDate(2026, time.January, 15)
	march   = ledger.MustDate(2026, time.March, 15)
	june    = ledger.MustDate(2026, time.June, 30)
	year    = ledger.MustDate(2026, time.January, 1)
)

func TestCurrentAdvancesIsWhoOwesWhat(t *testing.T) {
	svc, entries, _ := openTestService(t)

	lend(t, entries, 1, january, 5000)
	charge(t, entries, 1, march, 150)
	lend(t, entries, 2, march, 1000)
	repay(t, entries, 2, march, 1000, 0)
	// Customer 3 never borrowed.

	report, err := svc.CurrentAdvances(context.Background(), june)
	if err != nil {
		t.Fatalf("current advances: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("the report has %d rows, want the one farmer who owes something: %+v", len(report.Rows), report.Rows)
	}

	row := report.Rows[0]
	if row.Code != "BA-0001" || row.Principal.String() != "5000.00" ||
		row.Interest.String() != "150.00" || row.Total.String() != "5150.00" {
		t.Errorf("the row is %+v, want BA-0001 at 5000.00 + 150.00", row)
	}
	if report.Total.String() != "5150.00" {
		t.Errorf("the report totals %s, want 5150.00", report.Total)
	}
}

// A credit is money the station owes attention to. Hiding it would make the
// total on the page disagree with the ledger, which is the one thing a report
// must never do.
func TestCurrentAdvancesShowsACreditRatherThanHidingIt(t *testing.T) {
	svc, entries, _ := openTestService(t)

	lend(t, entries, 1, january, 1000)
	repay(t, entries, 1, march, 1500, 0)

	report, err := svc.CurrentAdvances(context.Background(), june)
	if err != nil {
		t.Fatalf("current advances: %v", err)
	}
	if len(report.Rows) != 1 || report.Rows[0].Total.String() != "-500.00" {
		t.Fatalf("got %+v, want one row in credit by 500.00", report.Rows)
	}
}

func TestROITotalsAreTheSumOfItsRows(t *testing.T) {
	svc, entries, db := openTestService(t)
	ctx := context.Background()

	lend(t, entries, 1, january, 5000)
	lend(t, entries, 2, january, 3000)
	charge(t, entries, 1, march, 150)
	settle(t, db, entries, 1, march, 2000, 150, ledger.Date{})
	settle(t, db, entries, 2, march, 3000, 0, ledger.Date{})

	report, err := svc.ROI(ctx, year, june)
	if err != nil {
		t.Fatalf("roi: %v", err)
	}

	if report.Deployed.String() != "8000.00" {
		t.Errorf("deployed %s, want 8000.00", report.Deployed)
	}
	if report.Repaid.String() != "5000.00" || report.Returned.String() != "5150.00" {
		t.Errorf("repaid %s and returned %s, want 5000.00 and 5150.00", report.Repaid, report.Returned)
	}
	if report.InterestCharged.String() != "150.00" || report.InterestCollected.String() != "150.00" {
		t.Errorf("interest %s charged and %s collected, want 150.00 and 150.00",
			report.InterestCharged, report.InterestCollected)
	}
	if report.Outstanding.String() != "3000.00" {
		t.Errorf("outstanding %s, want 3000.00", report.Outstanding)
	}
	if report.RepaidShare != "62.5%" {
		t.Errorf("the recovery rate reads %q, want 62.5%%", report.RepaidShare)
	}
	if report.Farmers != 2 || len(report.Rows) != 2 {
		t.Errorf("%d farmers over %d rows, want 2 and 2", report.Farmers, len(report.Rows))
	}

	// The invariant: the rows add up to the totals, exactly.
	var repaid, interest, returned money.Money
	for _, row := range report.Rows {
		repaid, _ = repaid.Add(row.Principal)
		interest, _ = interest.Add(row.Interest)
		returned, _ = returned.Add(row.Amount)
	}
	if repaid != report.Repaid || interest != report.InterestCollected || returned != report.Returned {
		t.Errorf("the rows sum to %s / %s / %s and the totals say %s / %s / %s",
			repaid, interest, returned, report.Repaid, report.InterestCollected, report.Returned)
	}
}

// Somebody who did nothing this period can still owe money, and a report that
// left them out of the outstanding figure would understate the book.
func TestROICountsMoneyStillOutFromBeforeThePeriod(t *testing.T) {
	svc, entries, _ := openTestService(t)

	lend(t, entries, 1, ledger.MustDate(2025, time.November, 1), 5000)
	lend(t, entries, 2, march, 1000)

	report, err := svc.ROI(context.Background(), year, june)
	if err != nil {
		t.Fatalf("roi: %v", err)
	}
	if report.Deployed.String() != "1000.00" {
		t.Errorf("deployed %s this year, want 1000.00", report.Deployed)
	}
	if report.Outstanding.String() != "6000.00" {
		t.Errorf("outstanding %s, want 6000.00 — last year's advance is still out", report.Outstanding)
	}
	if report.Farmers != 0 {
		t.Errorf("%d farmers settled this period, want none", report.Farmers)
	}
}

func TestAgingBucketsSumToWhatIsOutstanding(t *testing.T) {
	svc, entries, _ := openTestService(t)

	lend(t, entries, 1, june.AddDays(-200), 4000)
	lend(t, entries, 1, june.AddDays(-10), 1000)
	lend(t, entries, 2, june.AddDays(-45), 2000)
	repay(t, entries, 1, june.AddDays(-5), 1500, 0)
	charge(t, entries, 2, june, 60)

	report, err := svc.Aging(context.Background(), june)
	if err != nil {
		t.Fatalf("aging: %v", err)
	}

	// Customer 1: 4,000 at 200 days less a 1,500 payment against the oldest
	// money leaves 2,500 over 90 days, plus 1,000 current.
	if report.Over90.String() != "2500.00" || report.Current.String() != "1000.00" {
		t.Errorf("buckets are current %s and over-90 %s, want 1000.00 and 2500.00",
			report.Current, report.Over90)
	}
	if report.Days60.String() != "2000.00" {
		t.Errorf("the 31–60 bucket is %s, want customer 2's 2000.00", report.Days60)
	}
	if report.Principal.String() != "5500.00" {
		t.Errorf("the buckets total %s, want 5500.00", report.Principal)
	}
	// Interest is beside the buckets, never in them.
	if report.Interest.String() != "60.00" {
		t.Errorf("outstanding interest reads %s, want 60.00", report.Interest)
	}
}

// "Liquidated" in the legacy system meant settled up on a date, not paid off:
// a settlement that leaves money owing is still on the report.
func TestSettledListsEverySettlementIncludingOnesThatLeaveABalance(t *testing.T) {
	svc, entries, db := openTestService(t)

	lend(t, entries, 1, january, 5000)
	settle(t, db, entries, 1, march, 5000, 0, ledger.Date{})
	lend(t, entries, 2, january, 3000)
	settle(t, db, entries, 2, march, 1000, 0, ledger.Date{})

	report, err := svc.Settled(context.Background(), year, june)
	if err != nil {
		t.Fatalf("settled: %v", err)
	}
	if len(report.Rows) != 2 {
		t.Fatalf("got %+v, want both settlements", report.Rows)
	}
	// What each settlement left is worked out from what it stored as carried
	// and advanced, which this helper does not fill in; the API test checks
	// that figure end to end. Here it is enough that both are listed with what
	// they repaid.
	paid := map[string]string{}
	for _, row := range report.Rows {
		paid[row.Code] = row.PaidPrincipal.String()
	}
	if paid["BA-0001"] != "5000.00" || paid["BA-0002"] != "1000.00" {
		t.Errorf("the rows repaid %v, want BA-0001 5000.00 and BA-0002 1000.00", paid)
	}
}

// A settlement voided after the period still stood at the end of it, so it is
// on the report; one voided inside the period is not.
func TestSettledReadsAsTheBooksReadAtTheEndOfThePeriod(t *testing.T) {
	svc, entries, db := openTestService(t)

	lend(t, entries, 1, january, 5000)
	settle(t, db, entries, 1, march, 1000, 0, ledger.MustDate(2026, time.April, 1))
	lend(t, entries, 2, january, 5000)
	settle(t, db, entries, 2, march, 1000, 0, ledger.MustDate(2026, time.August, 1))

	report, err := svc.Settled(context.Background(), year, june)
	if err != nil {
		t.Fatalf("settled: %v", err)
	}
	if len(report.Rows) != 1 || report.Rows[0].Code != "BA-0002" {
		t.Fatalf("got %+v, want only BA-0002, voided after June", report.Rows)
	}
}

// The rule the whole step turns on, and the exact shape of it: a voided document
// leaves no trace in a report whose period reaches the void — and still appears
// in one that stops before it, because that is what the books said on that day.
//
// A reversal is dated the day of the void, which here is today: well after June.
// So the June reports still carry the mistake, and the reports to today do not.
func TestAVoidedVoucherLeavesNoTraceOnceTheVoidHasHappened(t *testing.T) {
	svc, entries, _ := openTestService(t)
	ctx := context.Background()

	lend(t, entries, 1, january, 5000)

	mistake := lend(t, entries, 2, march, 9999)
	if _, err := entries.Reverse(ctx, mistake, manager, "wrong farmer"); err != nil {
		t.Fatalf("void: %v", err)
	}

	// As at 30 June, before the void: the books said the money was out.
	asAtJune, err := svc.CurrentAdvances(ctx, june)
	if err != nil {
		t.Fatalf("current advances: %v", err)
	}
	if len(asAtJune.Rows) != 2 {
		t.Fatalf("as at June the report shows %+v; on that day the void had not happened", asAtJune.Rows)
	}

	// As at today, after it: gone from every one of them.
	today := ledger.DateOf(time.Now().In(station{}.Location()))

	current, err := svc.CurrentAdvances(ctx, today)
	if err != nil {
		t.Fatalf("current advances: %v", err)
	}
	if len(current.Rows) != 1 || current.Total.String() != "5000.00" {
		t.Errorf("current advances shows %+v totalling %s, want only the 5000.00 that stands",
			current.Rows, current.Total)
	}

	roi, err := svc.ROI(ctx, year, today)
	if err != nil {
		t.Fatalf("roi: %v", err)
	}
	if roi.Deployed.String() != "5000.00" {
		t.Errorf("roi deployed %s, want 5000.00", roi.Deployed)
	}

	aging, err := svc.Aging(ctx, today)
	if err != nil {
		t.Fatalf("aging: %v", err)
	}
	if aging.Principal.String() != "5000.00" {
		t.Errorf("aging totals %s, want 5000.00", aging.Principal)
	}

}

// Nothing out of nothing has no recovery rate. Printing "0.0%" would say the
// station recovered none of what it lent, which is a different claim.
func TestARecoveryRateOfNothingIsBlankRatherThanZero(t *testing.T) {
	svc, _, _ := openTestService(t)

	report, err := svc.ROI(context.Background(), year, june)
	if err != nil {
		t.Fatalf("roi: %v", err)
	}
	if report.RepaidShare != "" {
		t.Errorf("a period with nothing lent reports a recovery rate of %q", report.RepaidShare)
	}
}
