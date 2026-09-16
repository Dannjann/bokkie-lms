package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	bokkiedb "github.com/Dannjann/bokkie-lms/internal/db"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// station is the station's clock in tests: a fixed +08:00, so no test depends
// on the tz database being present or on where the machine running it is.
type station struct{}

func (station) Location() *time.Location { return time.FixedZone("PHT", 8*60*60) }

// openTestService returns a ledger over a fresh on-disk database with three
// customers already in it, and a clock frozen at a known instant so a
// reversal's date is something a test can assert on.
//
// It is on disk rather than in memory because that is what the station runs,
// and because the append-only triggers are the point of several of these tests:
// a test that proves them against a different engine proves nothing.
func openTestService(t *testing.T) (*Service, *sql.DB) {
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

	// created_by is a real foreign key, so the ledger cannot be exercised
	// without the people who wrote the entries actually existing.
	addUser(t, database, 1, "dan", "encoder")
	addUser(t, database, 2, "manager", "manager")

	for id := int64(1); id <= 3; id++ {
		addCustomer(t, database, id)
	}

	svc := New(database, station{}, audit.New(database, log), log)
	svc.now = func() time.Time { return time.Date(2026, 3, 2, 9, 30, 0, 0, svc.station.Location()) }
	return svc, database
}

func addCustomer(t *testing.T, database *sql.DB, id int64) {
	t.Helper()
	_, err := database.Exec(
		`INSERT INTO customers (id, code, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		id, fmt.Sprintf("BA-%04d", id), fmt.Sprintf("Customer %d", id),
		"2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("add customer %d: %v", id, err)
	}
}

func addUser(t *testing.T, database *sql.DB, id int64, username, role string) {
	t.Helper()
	_, err := database.Exec(
		`INSERT INTO users (id, username, display_name, password_hash, role, created_at)
		 VALUES (?, ?, ?, 'x', ?, '2026-01-01T00:00:00Z')`,
		id, username, username, role)
	if err != nil {
		t.Fatalf("add user %q: %v", username, err)
	}
}

// advance is the entry these tests reach for most often.
func advance(customerID int64, on Date, pesos int64) Entry {
	amount, err := money.FromPesos(pesos)
	if err != nil {
		panic(err)
	}
	return Entry{
		CustomerID: customerID,
		Date:       on,
		Type:       TypeAdvance,
		Principal:  amount,
		By:         Actor{UserID: 1, Username: "dan"},
	}
}

func payment(customerID int64, on Date, principal, interest int64) Entry {
	p, err := money.FromPesos(-principal)
	if err != nil {
		panic(err)
	}
	i, err := money.FromPesos(-interest)
	if err != nil {
		panic(err)
	}
	return Entry{
		CustomerID: customerID,
		Date:       on,
		Type:       TypePayment,
		Principal:  p,
		Interest:   i,
		By:         Actor{UserID: 1, Username: "dan"},
	}
}

func mustPost(t *testing.T, svc *Service, e Entry) int64 {
	t.Helper()
	id, err := svc.Post(context.Background(), e)
	if err != nil {
		t.Fatalf("post a %s entry: %v", e.Type, err)
	}
	return id
}

func balance(t *testing.T, svc *Service, customerID int64) Balance {
	t.Helper()
	b, err := svc.BalanceFor(context.Background(), customerID)
	if err != nil {
		t.Fatalf("balance for customer %d: %v", customerID, err)
	}
	return b
}

func TestPostingMovesTheBalance(t *testing.T) {
	svc, _ := openTestService(t)

	mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))
	mustPost(t, svc, advance(1, MustDate(2026, time.January, 20), 2500))
	mustPost(t, svc, payment(1, MustDate(2026, time.February, 1), 1000, 0))
	mustPost(t, svc, Entry{
		CustomerID: 1, Date: MustDate(2026, time.February, 1), Type: TypeInterest,
		Interest: money.FromCentavos(37500), By: Actor{UserID: 1, Username: "dan"},
	})

	got := balance(t, svc, 1)
	if got.Principal.Centavos() != 650000 {
		t.Errorf("principal = %s, want 6500.00", got.Principal)
	}
	if got.Interest.Centavos() != 37500 {
		t.Errorf("interest = %s, want 375.00", got.Interest)
	}
	if want := MustDate(2026, time.February, 1); !got.LastActivity.Equal(want) {
		t.Errorf("last activity = %s, want %s", got.LastActivity, want)
	}

	// A customer nothing has happened to is zero, not an error.
	if untouched := balance(t, svc, 3); !untouched.Principal.IsZero() || !untouched.LastActivity.IsZero() {
		t.Errorf("an untouched customer has balance %s/%s on %s",
			untouched.Principal, untouched.Interest, untouched.LastActivity)
	}
}

func TestPostRefusesEntriesThatMakeNoSense(t *testing.T) {
	svc, _ := openTestService(t)
	day := MustDate(2026, time.January, 10)
	who := Actor{UserID: 1, Username: "dan"}

	refused := map[string]Entry{
		"no customer":         {Date: day, Type: TypeAdvance, Principal: money.FromCentavos(100), By: who},
		"no date":             {CustomerID: 1, Type: TypeAdvance, Principal: money.FromCentavos(100), By: who},
		"nobody":              {CustomerID: 1, Date: day, Type: TypeAdvance, Principal: money.FromCentavos(100)},
		"no money":            {CustomerID: 1, Date: day, Type: TypeAdvance, By: who},
		"unknown type":        {CustomerID: 1, Date: day, Type: "guess", Principal: money.FromCentavos(100), By: who},
		"advance that repays": {CustomerID: 1, Date: day, Type: TypeAdvance, Principal: money.FromCentavos(-100), By: who},
		"advance with interest": {CustomerID: 1, Date: day, Type: TypeAdvance,
			Principal: money.FromCentavos(100), Interest: money.FromCentavos(5), By: who},
		"payment that lends": {CustomerID: 1, Date: day, Type: TypePayment, Principal: money.FromCentavos(100), By: who},
		"interest on principal": {CustomerID: 1, Date: day, Type: TypeInterest,
			Principal: money.FromCentavos(100), Interest: money.FromCentavos(5), By: who},
		"adjustment with no reason": {CustomerID: 1, Date: day, Type: TypeAdjustment,
			Principal: money.FromCentavos(100), By: who},
		"half a provenance": {CustomerID: 1, Date: day, Type: TypeAdvance,
			Principal: money.FromCentavos(100), SourceTable: "advance_lines", By: who},
		"a reversal by the front door": {CustomerID: 1, Date: day, Type: TypeReversal,
			Principal: money.FromCentavos(-100), ReversesID: 1, Reason: "typo", By: who},
	}

	for name, entry := range refused {
		if _, err := svc.Post(context.Background(), entry); err == nil {
			t.Errorf("%s: posted, want a refusal", name)
		} else if !errors.Is(err, ErrInvalidEntry) {
			t.Errorf("%s: %v, want ErrInvalidEntry", name, err)
		}
	}

	// And nothing reached the file.
	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM ledger_entries`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("%d entries were written by refused posts", count)
	}
}

func TestReversalUndoesExactlyAndDatesItself(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	january := MustDate(2026, time.January, 10)
	advanceID := mustPost(t, svc, advance(1, january, 5000))
	before := balance(t, svc, 1)

	mustPost(t, svc, advance(1, MustDate(2026, time.February, 15), 1200))
	afterSecond := balance(t, svc, 1)

	reversalID, err := svc.Reverse(ctx, advanceID, Actor{UserID: 2, Username: "manager"}, "wrong customer")
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}

	reversal, err := svc.EntryByID(ctx, reversalID)
	if err != nil {
		t.Fatalf("read the reversal: %v", err)
	}
	if reversal.Type != TypeReversal || reversal.ReversesID != advanceID {
		t.Errorf("reversal %d is a %s of %d", reversalID, reversal.Type, reversal.ReversesID)
	}
	if reversal.Principal.Centavos() != -500000 || !reversal.Interest.IsZero() {
		t.Errorf("reversal moves %s/%s, want -5000.00/0.00", reversal.Principal, reversal.Interest)
	}
	if reversal.Reason != "wrong customer" || reversal.By.Username != "manager" {
		t.Errorf("reversal says %q by %q", reversal.Reason, reversal.By.Username)
	}

	// Dated the day it was made, not the day of the entry it undoes: a
	// statement printed in February must still say in March what it said then.
	if want := MustDate(2026, time.March, 2); !reversal.Date.Equal(want) {
		t.Errorf("reversal dated %s, want %s", reversal.Date, want)
	}
	asOfFebruary, err := svc.BalanceAt(ctx, 1, MustDate(2026, time.February, 28))
	if err != nil {
		t.Fatalf("balance at February: %v", err)
	}
	if asOfFebruary.Principal != afterSecond.Principal {
		t.Errorf("the February balance became %s after a March reversal, was %s",
			asOfFebruary.Principal, afterSecond.Principal)
	}

	// And the live balance is back to exactly where it was before the reversed
	// entry — to the centavo, not within one.
	now := balance(t, svc, 1)
	want, err := afterSecond.Principal.Sub(money.FromCentavos(500000))
	if err != nil {
		t.Fatal(err)
	}
	if now.Principal != want {
		t.Errorf("principal after the reversal = %s, want %s", now.Principal, want)
	}
	if before.Principal.Centavos() != 500000 {
		t.Errorf("the balance before the second advance was %s", before.Principal)
	}
}

func TestAnEntryCanBeReversedOnlyOnce(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()
	who := Actor{UserID: 2, Username: "manager"}

	advanceID := mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))
	if _, err := svc.Reverse(ctx, advanceID, who, "typo"); err != nil {
		t.Fatalf("first reversal: %v", err)
	}

	before := balance(t, svc, 1)
	if _, err := svc.Reverse(ctx, advanceID, who, "again"); !errors.Is(err, ErrAlreadyReversed) {
		t.Errorf("second reversal gave %v, want ErrAlreadyReversed", err)
	}
	if after := balance(t, svc, 1); after != before {
		t.Errorf("the refused reversal changed the balance from %s to %s", before.Principal, after.Principal)
	}
}

func TestAReversalCannotItselfBeReversed(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()
	who := Actor{UserID: 2, Username: "manager"}

	advanceID := mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))
	reversalID, err := svc.Reverse(ctx, advanceID, who, "typo")
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}

	if _, err := svc.Reverse(ctx, reversalID, who, "undo the undo"); !errors.Is(err, ErrReversalOfReversal) {
		t.Errorf("reversing a reversal gave %v, want ErrReversalOfReversal", err)
	}
}

func TestReversingSomethingThatIsNotThere(t *testing.T) {
	svc, _ := openTestService(t)
	_, err := svc.Reverse(context.Background(), 999, Actor{UserID: 2, Username: "manager"}, "typo")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("reversing a missing entry gave %v, want ErrNotFound", err)
	}
}

// Half a posted voucher is a corruption, not a partial success — and the table
// it would be half-posted into cannot be edited to clean it up.
func TestPostBatchIsAllOrNothing(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()
	day := MustDate(2026, time.January, 10)

	mustPost(t, svc, advance(1, day, 100))
	before := balance(t, svc, 1)

	// Caught before the database is touched at all.
	if _, err := svc.PostBatch(ctx, []Entry{
		advance(1, day, 500),
		advance(2, day, 700),
		{CustomerID: 1, Date: day, Type: TypeAdvance, By: Actor{Username: "dan"}}, // moves no money
	}); err == nil {
		t.Error("a batch with an invalid entry was posted")
	}

	// And caught by the database, part-way through: customer 99 does not exist,
	// so the foreign key fails on the third insert after two have succeeded.
	if _, err := svc.PostBatch(ctx, []Entry{
		advance(1, day, 500),
		advance(2, day, 700),
		advance(99, day, 900),
	}); err == nil {
		t.Error("a batch naming a customer that does not exist was posted")
	}

	if after := balance(t, svc, 1); after != before {
		t.Errorf("a failed batch left customer 1 at %s, was %s", after.Principal, before.Principal)
	}
	if other := balance(t, svc, 2); !other.Principal.IsZero() {
		t.Errorf("a failed batch left customer 2 holding %s", other.Principal)
	}

	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM ledger_entries`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("the ledger holds %d entries, want only the one that was posted successfully", count)
	}
}

func TestPostBatchPostsOneVoucherAcrossSeveralCustomers(t *testing.T) {
	svc, _ := openTestService(t)
	day := MustDate(2026, time.January, 10)

	lines := []Entry{advance(1, day, 500), advance(2, day, 700), advance(1, day, 300)}
	for i := range lines {
		lines[i].SourceTable = "advance_lines"
		lines[i].SourceID = int64(i + 1)
	}

	ids, err := svc.PostBatch(context.Background(), lines)
	if err != nil {
		t.Fatalf("post the voucher: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("got %d ids for 3 lines", len(ids))
	}
	if got := balance(t, svc, 1).Principal.Centavos(); got != 80000 {
		t.Errorf("customer 1 holds %d centavos, want 80000", got)
	}
	if got := balance(t, svc, 2).Principal.Centavos(); got != 70000 {
		t.Errorf("customer 2 holds %d centavos, want 70000", got)
	}
}

func TestBalanceAtIsHistorical(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))
	mustPost(t, svc, advance(1, MustDate(2026, time.February, 14), 3000))
	mustPost(t, svc, payment(1, MustDate(2026, time.March, 1), 2000, 0))

	cases := map[string]int64{
		"2026-01-09": 0,
		"2026-01-10": 500000, // inclusive of the day itself
		"2026-02-13": 500000,
		"2026-02-14": 800000,
		"2026-03-01": 600000,
		"2026-12-31": 600000,
	}
	for on, want := range cases {
		day, err := ParseDate(on)
		if err != nil {
			t.Fatal(err)
		}
		got, err := svc.BalanceAt(ctx, 1, day)
		if err != nil {
			t.Fatalf("balance at %s: %v", on, err)
		}
		if got.Principal.Centavos() != want {
			t.Errorf("balance at %s = %s, want %d centavos", on, got.Principal, want)
		}
	}
}

func TestStatementIsTheRangeInOrder(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))
	mustPost(t, svc, advance(1, MustDate(2026, time.February, 14), 3000))
	mustPost(t, svc, advance(1, MustDate(2026, time.February, 14), 100)) // same day, later
	mustPost(t, svc, payment(1, MustDate(2026, time.March, 1), 2000, 0))
	mustPost(t, svc, advance(2, MustDate(2026, time.February, 1), 999)) // another customer

	entries, err := svc.StatementFor(ctx, 1,
		MustDate(2026, time.February, 1), MustDate(2026, time.February, 28))
	if err != nil {
		t.Fatalf("statement: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries in February, want 2", len(entries))
	}
	if entries[0].ID > entries[1].ID {
		t.Error("two entries on the same day came back in an unstable order")
	}
	for _, e := range entries {
		if e.CustomerID != 1 {
			t.Errorf("entry %d belongs to customer %d", e.ID, e.CustomerID)
		}
	}

	// An open-ended statement runs from the beginning to today.
	all, err := svc.StatementFor(ctx, 1, Date{}, Date{})
	if err != nil {
		t.Fatalf("full statement: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("the full statement has %d entries, want 4", len(all))
	}
}

// The protection that matters is the one that still works when somebody opens
// bokkie.db in a SQLite browser, so this goes around the service entirely.
func TestTheDatabaseItselfRefusesToEditTheLedger(t *testing.T) {
	svc, database := openTestService(t)
	id := mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))

	if _, err := database.Exec(
		`UPDATE ledger_entries SET principal_delta_cents = 1 WHERE id = ?`, id); err == nil {
		t.Error("an UPDATE against ledger_entries succeeded")
	} else if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("the UPDATE failed with %v, which does not explain why", err)
	}

	if _, err := database.Exec(`DELETE FROM ledger_entries WHERE id = ?`, id); err == nil {
		t.Error("a DELETE against ledger_entries succeeded")
	} else if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("the DELETE failed with %v, which does not explain why", err)
	}

	// A hand-written reversal that is not the exact negation is refused too.
	if _, err := database.Exec(`
		INSERT INTO ledger_entries
		    (customer_id, entry_date, entry_type, principal_delta_cents, interest_delta_cents,
		     reverses_id, reason, created_at, created_by_username)
		VALUES (1, '2026-03-02', 'reversal', -1, 0, ?, 'close enough', '2026-03-02T09:30:00Z', 'sqlite3')`,
		id); err == nil {
		t.Error("a reversal that was not the exact negation was accepted")
	}
}

func TestCorruptedCacheIsDetectedRebuiltAndAudited(t *testing.T) {
	svc, database := openTestService(t)
	ctx := context.Background()

	mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))
	mustPost(t, svc, advance(2, MustDate(2026, time.January, 11), 2500))

	// customer_balances is the one part of the ledger that can be written to,
	// which is exactly why it is the one part that has to be checked.
	if _, err := database.Exec(
		`UPDATE customer_balances SET principal_cents = 1 WHERE customer_id = 1`); err != nil {
		t.Fatalf("corrupt the cache: %v", err)
	}
	if _, err := database.Exec(`DELETE FROM customer_balances WHERE customer_id = 2`); err != nil {
		t.Fatalf("delete a cache row: %v", err)
	}

	found, err := svc.SelfCheck(ctx, Console)
	if err != nil {
		t.Fatalf("self-check: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("the self-check found %d discrepancies, want 2: %v", len(found), found)
	}

	if got := balance(t, svc, 1).Principal.Centavos(); got != 500000 {
		t.Errorf("customer 1 was rebuilt to %d centavos, want 500000", got)
	}
	if got := balance(t, svc, 2).Principal.Centavos(); got != 250000 {
		t.Errorf("customer 2 was rebuilt to %d centavos, want 250000", got)
	}

	var actions int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM audit_log WHERE action = ?`, audit.ActionLedgerCacheRebuilt).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if actions != 1 {
		t.Errorf("the rebuild left %d audit rows, want 1", actions)
	}

	// A second check finds nothing, and writes nothing.
	if found, err := svc.SelfCheck(ctx, Console); err != nil || len(found) != 0 {
		t.Errorf("the second self-check found %v (%v)", found, err)
	}
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM audit_log WHERE action = ?`, audit.ActionLedgerCacheRebuilt).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if actions != 1 {
		t.Errorf("a clean self-check wrote an audit row; there are now %d", actions)
	}
}

func TestCheckOnStartStandsAsideForALargeLedger(t *testing.T) {
	svc, database := openTestService(t)
	mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))
	if _, err := database.Exec(`UPDATE customer_balances SET principal_cents = 1`); err != nil {
		t.Fatal(err)
	}

	// Below the limit it corrects before the port opens.
	if err := svc.CheckOnStart(context.Background()); err != nil {
		t.Fatalf("check on start: %v", err)
	}
	if got := balance(t, svc, 1).Principal.Centavos(); got != 500000 {
		t.Errorf("the boot check left customer 1 at %d centavos", got)
	}
}

// The service checks for an existing reversal before writing one, so this test
// goes around the service entirely.
//
// That Go check is what produces a sentence a manager can read. It is not what
// makes the rule true: two managers voiding the same voucher at the same moment
// would both pass it, and the thing standing between them and a double credit
// is the unique index.
func TestTheDatabaseRefusesASecondReversal(t *testing.T) {
	svc, database := openTestService(t)
	ctx := context.Background()

	advanceID := mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))
	if _, err := svc.Reverse(ctx, advanceID, Actor{UserID: 2, Username: "manager"}, "typo"); err != nil {
		t.Fatalf("first reversal: %v", err)
	}

	_, err := database.Exec(`
		INSERT INTO ledger_entries
		    (customer_id, entry_date, entry_type, principal_delta_cents, interest_delta_cents,
		     reverses_id, reason, created_at, created_by_username)
		VALUES (1, '2026-03-02', 'reversal', -500000, 0, ?, 'the same void again', '2026-03-02T09:30:00Z', 'sqlite3')`,
		advanceID)
	if err == nil {
		t.Fatal("a second reversal of the same entry was written straight to the database")
	}
	if !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("the second reversal was refused by %v, not by the unique index", err)
	}
}

// Two counters posting at the same moment is the ordinary case, not the
// exceptional one: the station has several PCs and one database.
func TestConcurrentPostsProduceCorrectBalances(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	const writers, each = 8, 10
	errs := make(chan error, writers)

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				customer := int64(i%3) + 1
				if _, err := svc.Post(ctx, advance(customer, MustDate(2026, time.January, 10), 100)); err != nil {
					errs <- fmt.Errorf("writer %d, post %d: %w", w, i, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	var total int64
	for id := int64(1); id <= 3; id++ {
		total += balance(t, svc, id).Principal.Centavos()
	}
	if want := int64(writers * each * 10000); total != want {
		t.Errorf("concurrent posts added up to %d centavos, want %d", total, want)
	}

	// And the cache kept up with every one of them.
	found, err := svc.Discrepancies(ctx)
	if err != nil {
		t.Fatalf("compare the cache against the ledger: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("the cache drifted under concurrent posts: %v", found)
	}
}

// The transaction-taking variants exist so that a document and the entries it
// produces commit together. These three tests are what "together" means: work
// done through them is gone when the caller's transaction rolls back, and no
// second transaction of the ledger's own has quietly committed half of it.

func TestPostBatchTxJoinsTheCallersTransaction(t *testing.T) {
	svc, database := openTestService(t)
	ctx := context.Background()
	day := MustDate(2026, time.January, 10)

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if _, err := svc.PostBatchTx(ctx, tx, []Entry{advance(1, day, 500), advance(2, day, 700)}); err != nil {
		t.Fatalf("post inside the caller's transaction: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("roll back: %v", err)
	}

	if count := entryCount(t, database); count != 0 {
		t.Errorf("the ledger holds %d entries after a rolled-back post, want 0", count)
	}
	for _, id := range []int64{1, 2} {
		if got := balance(t, svc, id); !got.Principal.IsZero() {
			t.Errorf("customer %d holds %s after a rolled-back post, want nothing", id, got.Principal)
		}
	}

	var cached int
	if err := database.QueryRow(`SELECT COUNT(*) FROM customer_balances`).Scan(&cached); err != nil {
		t.Fatal(err)
	}
	if cached != 0 {
		t.Errorf("a rolled-back post left %d cached balances behind", cached)
	}
}

func TestReverseTxJoinsTheCallersTransaction(t *testing.T) {
	svc, database := openTestService(t)
	ctx := context.Background()
	day := MustDate(2026, time.January, 10)

	entryID := mustPost(t, svc, advance(1, day, 500))
	before := balance(t, svc, 1)

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := svc.ReverseTx(ctx, tx, entryID, Actor{UserID: 2, Username: "manager"}, "voided the voucher"); err != nil {
		t.Fatalf("reverse inside the caller's transaction: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("roll back: %v", err)
	}

	if count := entryCount(t, database); count != 1 {
		t.Errorf("the ledger holds %d entries after a rolled-back void, want only the advance", count)
	}
	if after := balance(t, svc, 1); after != before {
		t.Errorf("a rolled-back void left customer 1 at %s, was %s", after.Principal, before.Principal)
	}

	// And the entry is still reversible: a rolled-back void must not have used
	// up the one reversal an entry is allowed.
	if _, err := svc.Reverse(ctx, entryID, Actor{UserID: 2, Username: "manager"}, "voided the voucher"); err != nil {
		t.Errorf("reversing after a rolled-back void: %v", err)
	}
}

func TestEntriesBySourceFindsWhatADocumentWrote(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()
	day := MustDate(2026, time.January, 10)

	// Two documents: a voucher with three lines, and an unrelated one whose
	// entry must not come back with them.
	voucher := []Entry{advance(1, day, 500), advance(2, day, 700), advance(1, day, 300)}
	for i := range voucher {
		voucher[i].SourceTable = "advance_lines"
		voucher[i].SourceID = int64(i + 1)
	}
	ids, err := svc.PostBatch(ctx, voucher)
	if err != nil {
		t.Fatalf("post the voucher: %v", err)
	}

	other := advance(3, day, 900)
	other.SourceTable = "advance_lines"
	other.SourceID = 99
	mustPost(t, svc, other)

	lineIDs := []int64{1, 2, 3}
	found, err := svc.EntriesBySource(ctx, "advance_lines", lineIDs)
	if err != nil {
		t.Fatalf("entries by source: %v", err)
	}
	if len(found) != 3 {
		t.Fatalf("got %d entries for a three-line voucher, want 3", len(found))
	}
	for i, entry := range found {
		if entry.ID != ids[i] {
			t.Errorf("entry %d is id %d, want %d — the order is the order they were written", i, entry.ID, ids[i])
		}
	}

	// A reversal carries its target's provenance, so voiding a line adds to
	// what the document is shown to have done rather than removing from it.
	if _, err := svc.Reverse(ctx, ids[0], Actor{UserID: 2, Username: "manager"}, "wrong amount"); err != nil {
		t.Fatalf("reverse a line: %v", err)
	}
	found, err = svc.EntriesBySource(ctx, "advance_lines", lineIDs)
	if err != nil {
		t.Fatalf("entries by source after a void: %v", err)
	}
	if len(found) != 4 {
		t.Fatalf("got %d entries after voiding one line, want 4 — the reversal belongs to the document too", len(found))
	}
	if !found[3].IsReversal() || found[3].ReversesID != ids[0] {
		t.Errorf("the last entry is %s reversing %d, want a reversal of %d", found[3].Type, found[3].ReversesID, ids[0])
	}
}

func TestEntriesBySourceAsksNothingOfAnEmptyDocument(t *testing.T) {
	svc, _ := openTestService(t)

	found, err := svc.EntriesBySource(context.Background(), "advance_lines", nil)
	if err != nil {
		t.Fatalf("entries by source with no ids: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("got %d entries for no ids", len(found))
	}
	if _, err := svc.EntriesBySource(context.Background(), "", []int64{1}); err == nil {
		t.Error("entries were looked up with no source table named")
	}
}

func entryCount(t *testing.T, database *sql.DB) int {
	t.Helper()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ledger_entries`).Scan(&count); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	return count
}

func TestAStatementOpensWhereThePreviousOneClosed(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))
	mustPost(t, svc, advance(1, MustDate(2026, time.February, 14), 3000))
	mustPost(t, svc, payment(1, MustDate(2026, time.March, 1), 2000, 0))

	stmt, err := svc.Statement(ctx, 1, MustDate(2026, time.February, 1), MustDate(2026, time.March, 31))
	if err != nil {
		t.Fatalf("statement: %v", err)
	}

	// Without an opening balance a statement of the last month reads as though
	// the farmer started it owing nothing.
	if stmt.Opening.Principal.Centavos() != 500000 {
		t.Errorf("the statement opens at %s, want the ₱5,000.00 owed before February", stmt.Opening.Principal)
	}
	if len(stmt.Rows) != 2 {
		t.Fatalf("the statement has %d rows, want 2", len(stmt.Rows))
	}

	// The running balance counts everything before the window, not just the
	// rows on the page.
	if stmt.Rows[0].Running.Principal.Centavos() != 800000 {
		t.Errorf("after the first row the balance runs to %s, want ₱8,000.00", stmt.Rows[0].Running.Principal)
	}
	if stmt.Rows[1].Running.Principal.Centavos() != 600000 {
		t.Errorf("after the second row the balance runs to %s, want ₱6,000.00", stmt.Rows[1].Running.Principal)
	}

	// The invariant: the last line agrees with the balance the rest of the
	// system shows.
	at, err := svc.BalanceAt(ctx, 1, MustDate(2026, time.March, 31))
	if err != nil {
		t.Fatal(err)
	}
	if stmt.Closing.Principal != at.Principal || stmt.Closing.Interest != at.Interest {
		t.Errorf("the statement closes at %s/%s, and BalanceAt says %s/%s",
			stmt.Closing.Principal, stmt.Closing.Interest, at.Principal, at.Interest)
	}

	movement, err := stmt.Movement()
	if err != nil {
		t.Fatal(err)
	}
	if movement.Principal.Centavos() != 100000 {
		t.Errorf("the period moved %s of principal, want ₱1,000.00 — ₱3,000.00 lent less ₱2,000.00 paid",
			movement.Principal)
	}
}

// A window in which nothing happened still closes where it opened, rather than
// at zero.
func TestAnEmptyWindowStillCarriesTheBalance(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	mustPost(t, svc, advance(1, MustDate(2026, time.January, 10), 5000))

	stmt, err := svc.Statement(ctx, 1, MustDate(2026, time.February, 1), MustDate(2026, time.February, 28))
	if err != nil {
		t.Fatalf("statement: %v", err)
	}
	if len(stmt.Rows) != 0 {
		t.Fatalf("a quiet February has %d rows", len(stmt.Rows))
	}
	if stmt.Closing.Principal.Centavos() != 500000 || stmt.Opening.Principal != stmt.Closing.Principal {
		t.Errorf("a quiet month opened at %s and closed at %s", stmt.Opening.Principal, stmt.Closing.Principal)
	}
}

// A customer nothing has ever happened to is an empty statement, not an error.
func TestAStatementOfACustomerWithNoEntries(t *testing.T) {
	svc, _ := openTestService(t)

	stmt, err := svc.Statement(context.Background(), 3, Date{}, Date{})
	if err != nil {
		t.Fatalf("statement: %v", err)
	}
	if len(stmt.Rows) != 0 || !stmt.Closing.Principal.IsZero() {
		t.Errorf("a customer with no entries has %d rows and owes %s", len(stmt.Rows), stmt.Closing.Principal)
	}
}

// A statement that hides corrections is a statement that lies. The farmer
// holding the paper slip is the person most entitled to see that something was
// undone.
func TestAStatementShowsReversals(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	id := mustPost(t, svc, advance(1, MustDate(2026, time.February, 10), 5000))
	if _, err := svc.Reverse(ctx, id, Actor{UserID: 2, Username: "manager"}, "wrong farmer"); err != nil {
		t.Fatalf("reverse: %v", err)
	}

	stmt, err := svc.Statement(ctx, 1, Date{}, Date{})
	if err != nil {
		t.Fatalf("statement: %v", err)
	}
	if len(stmt.Rows) != 2 {
		t.Fatalf("the statement has %d rows, want the advance and its reversal", len(stmt.Rows))
	}
	if !stmt.Rows[1].Entry.IsReversal() || stmt.Rows[1].Entry.Reason != "wrong farmer" {
		t.Errorf("the second row is a %s for %q", stmt.Rows[1].Entry.Type, stmt.Rows[1].Entry.Reason)
	}
	if !stmt.Closing.Principal.IsZero() {
		t.Errorf("after a reversal the statement closes at %s, want nothing", stmt.Closing.Principal)
	}
}

func TestAStatementCannotRunBackwards(t *testing.T) {
	svc, _ := openTestService(t)

	_, err := svc.Statement(context.Background(), 1,
		MustDate(2026, time.March, 31), MustDate(2026, time.March, 1))
	if err == nil {
		t.Error("a statement ran from March 31 back to March 1")
	}
}
