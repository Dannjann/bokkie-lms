package advance

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// station is the station in tests: a clock fixed at +08:00, so nothing here
// depends on the tz database or on where the machine running it is, and a
// Ref. No. sequence that starts wherever a test needs it to.
type station struct{ refNoStart int }

func (station) Location() *time.Location { return time.FixedZone("PHT", 8*60*60) }

func (s station) Int(d settings.Def) int {
	if d.Key == settings.RefNoStart.Key && s.refNoStart > 0 {
		return s.refNoStart
	}
	n, _ := strconv.Atoi(d.Default)
	return n
}

// today is the day the test clock is frozen on. Dates in tests are written
// relative to it, because "not in the future" is one of the rules under test.
var today = ledger.MustDate(2026, time.March, 2)

func openTestService(t *testing.T) (*Service, *ledger.Service, *sql.DB) {
	t.Helper()

	return openStationService(t, station{})
}

// openStationService is openTestService with the station's settings chosen by
// the test — which today means where the Ref. No. sequence starts.
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

// draftOf builds a voucher draft for the given customers, one line each, in
// pesos. Most tests care about what happens to a voucher rather than about what
// is on it.
func draftOf(t *testing.T, lines ...int64) Draft {
	t.Helper()

	d := Draft{Date: today, Notes: "palay advances"}
	for i := 0; i < len(lines); i += 2 {
		amount, err := money.FromPesos(lines[i+1])
		if err != nil {
			t.Fatalf("build an amount: %v", err)
		}
		d.Lines = append(d.Lines, DraftLine{
			CustomerID: lines[i],
			Particular: "palay advance",
			Amount:     amount,
		})
	}
	return d
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

func mustCreate(t *testing.T, svc *Service, database *sql.DB, d Draft) Voucher {
	t.Helper()

	var v Voucher
	err := inTx(t, database, func(tx *sql.Tx) error {
		var err error
		v, err = svc.CreateTx(context.Background(), tx, d, dan)
		return err
	})
	if err != nil {
		t.Fatalf("create a draft: %v", err)
	}
	return v
}

var (
	dan     = Actor{UserID: 1, Username: "dan"}
	manager = Actor{UserID: 2, Username: "manager"}
)

func TestADraftIsSavedWithNoNumberAndNoMoneyMoved(t *testing.T) {
	svc, entries, database := openTestService(t)
	ctx := context.Background()

	v := mustCreate(t, svc, database, draftOf(t, 1, 500, 2, 700))

	if v.Status != StatusDraft {
		t.Errorf("a new voucher is %s, want draft", v.Status)
	}
	if v.RefNo != 0 {
		t.Errorf("a draft was given Ref. No. %d — numbering happens at posting", v.RefNo)
	}
	if v.PostedAt != nil {
		t.Errorf("a draft carries a posted_at of %v", v.PostedAt)
	}
	if len(v.Lines) != 2 {
		t.Fatalf("a two-line draft came back with %d lines", len(v.Lines))
	}
	if got := v.Total.Centavos(); got != 120000 {
		t.Errorf("the draft totals %d centavos, want 120000", got)
	}
	if v.Lines[0].CustomerCode != "BA-0001" {
		t.Errorf("line 1 is for %q, want BA-0001", v.Lines[0].CustomerCode)
	}
	if !v.Lines[0].Date.Equal(today) {
		t.Errorf("a line with no date of its own is dated %s, want the voucher's %s", v.Lines[0].Date, today)
	}

	// The whole point of a draft: it has moved nothing.
	for _, id := range []int64{1, 2} {
		b, err := entries.BalanceFor(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !b.Principal.IsZero() {
			t.Errorf("customer %d owes %s off a draft", id, b.Principal)
		}
	}
}

func TestADraftIsEdited(t *testing.T) {
	svc, _, database := openTestService(t)
	ctx := context.Background()

	v := mustCreate(t, svc, database, draftOf(t, 1, 500, 2, 700))

	edited := draftOf(t, 3, 900)
	edited.Notes = "corrected"
	edited.Date = today.AddDays(-1)

	var after Voucher
	err := inTx(t, database, func(tx *sql.Tx) error {
		var err error
		after, err = svc.UpdateTx(ctx, tx, v.ID, edited)
		return err
	})
	if err != nil {
		t.Fatalf("edit a draft: %v", err)
	}

	if len(after.Lines) != 1 || after.Lines[0].CustomerCode != "BA-0003" {
		t.Fatalf("editing left %d lines, want the one it was given", len(after.Lines))
	}
	if after.Notes != "corrected" || !after.Date.Equal(today.AddDays(-1)) {
		t.Errorf("the header was not updated: %q on %s", after.Notes, after.Date)
	}
	if got := after.Total.Centavos(); got != 90000 {
		t.Errorf("the edited draft totals %d centavos, want 90000", got)
	}

	// Replacing the lines means the old ones are gone, not orphaned.
	var lines int
	if err := database.QueryRow(`SELECT COUNT(*) FROM advance_lines`).Scan(&lines); err != nil {
		t.Fatal(err)
	}
	if lines != 1 {
		t.Errorf("the table holds %d lines after an edit, want 1", lines)
	}
}

func TestADraftIsDeletedOutright(t *testing.T) {
	svc, _, database := openTestService(t)
	ctx := context.Background()

	v := mustCreate(t, svc, database, draftOf(t, 1, 500, 2, 700))

	if err := inTx(t, database, func(tx *sql.Tx) error {
		return svc.DeleteTx(ctx, tx, v.ID)
	}); err != nil {
		t.Fatalf("delete a draft: %v", err)
	}

	if _, err := svc.ByID(ctx, v.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading a deleted draft gave %v, want ErrNotFound", err)
	}
	var lines int
	if err := database.QueryRow(`SELECT COUNT(*) FROM advance_lines`).Scan(&lines); err != nil {
		t.Fatal(err)
	}
	if lines != 0 {
		t.Errorf("deleting a draft left %d of its lines behind", lines)
	}
}

func TestWhatADraftRefuses(t *testing.T) {
	svc, _, database := openTestService(t)
	ctx := context.Background()

	// A deactivated customer cannot be added to a voucher, though one already
	// named on a draft can still be posted later.
	if _, err := database.Exec(`UPDATE customers SET active = 0 WHERE id = 3`); err != nil {
		t.Fatal(err)
	}

	future := draftOf(t, 1, 500)
	future.Date = today.AddDays(1)

	futureLine := draftOf(t, 1, 500)
	futureLine.Lines[0].Date = today.AddDays(1)

	zero := draftOf(t, 1, 0)

	negative := draftOf(t, 1, 500)
	negative.Lines[0].Amount = money.FromCentavos(-1)

	empty := draftOf(t)

	noDate := draftOf(t, 1, 500)
	noDate.Date = ledger.Date{}

	for _, tc := range []struct {
		name  string
		draft Draft
		field string
	}{
		{"a voucher dated tomorrow", future, "date"},
		{"a line dated tomorrow", futureLine, "lines[0].date"},
		{"a line worth nothing", zero, "lines[0].amount"},
		{"a line worth less than nothing", negative, "lines[0].amount"},
		{"a voucher with no lines", empty, "lines"},
		{"a voucher with no date", noDate, "date"},
		{"a line for a customer who is not there", draftOf(t, 99, 500), "lines[0].customer_id"},
		{"a line for a deactivated customer", draftOf(t, 3, 500), "lines[0].customer_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := inTx(t, database, func(tx *sql.Tx) error {
				_, err := svc.CreateTx(ctx, tx, tc.draft, dan)
				return err
			})

			var ve ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("got %v, want a ValidationError a counter clerk could act on", err)
			}
			if ve.Field != tc.field {
				t.Errorf("the complaint names %q, want %q", ve.Field, tc.field)
			}
			if ve.Message == "" {
				t.Error("the complaint has no message")
			}
		})
	}

	// And nothing was written by any of them.
	var vouchers int
	if err := database.QueryRow(`SELECT COUNT(*) FROM advance_vouchers`).Scan(&vouchers); err != nil {
		t.Fatal(err)
	}
	if vouchers != 0 {
		t.Errorf("%d vouchers were written by drafts that were all refused", vouchers)
	}
}

func TestEditingAndDeletingSomethingThatIsNotThere(t *testing.T) {
	svc, _, database := openTestService(t)
	ctx := context.Background()

	err := inTx(t, database, func(tx *sql.Tx) error {
		_, err := svc.UpdateTx(ctx, tx, 404, draftOf(t, 1, 500))
		return err
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("editing a voucher that is not there gave %v, want ErrNotFound", err)
	}

	err = inTx(t, database, func(tx *sql.Tx) error {
		return svc.DeleteTx(ctx, tx, 404)
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting a voucher that is not there gave %v, want ErrNotFound", err)
	}
}

func TestListFindsVouchersByNumberAndByFarmer(t *testing.T) {
	svc, _, database := openTestService(t)
	ctx := context.Background()

	first := mustCreate(t, svc, database, draftOf(t, 1, 500))
	second := mustCreate(t, svc, database, draftOf(t, 2, 700, 3, 300))
	post(t, database, first.ID, 1425)

	all, err := svc.List(ctx, Query{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if all.Total != 2 {
		t.Fatalf("listing everything found %d vouchers, want 2", all.Total)
	}
	if all.Vouchers[0].ID != second.ID {
		t.Errorf("the first row is voucher %d, want the newest (%d)", all.Vouchers[0].ID, second.ID)
	}
	if got := all.Vouchers[0].Total.Centavos(); got != 100000 {
		t.Errorf("the two-line voucher totals %d centavos, want 100000", got)
	}
	if got := all.Vouchers[0].LineCount; got != 2 {
		t.Errorf("the two-line voucher reports %d lines", got)
	}
	if len(all.Vouchers[0].Customers) != 2 {
		t.Errorf("the two-line voucher names %v, want both farmers", all.Vouchers[0].Customers)
	}

	byRef, err := svc.List(ctx, Query{Text: "1425"})
	if err != nil {
		t.Fatalf("list by ref: %v", err)
	}
	if byRef.Total != 1 || byRef.Vouchers[0].ID != first.ID {
		t.Errorf("searching 1425 found %d vouchers, want only the one numbered 1425", byRef.Total)
	}

	byName, err := svc.List(ctx, Query{Text: "Customer 3"})
	if err != nil {
		t.Fatalf("list by name: %v", err)
	}
	if byName.Total != 1 || byName.Vouchers[0].ID != second.ID {
		t.Errorf("searching a farmer's name found %d vouchers, want the one with them on a line", byName.Total)
	}

	drafts, err := svc.List(ctx, Query{Status: StatusDraft})
	if err != nil {
		t.Fatalf("list drafts: %v", err)
	}
	if drafts.Total != 1 || drafts.Vouchers[0].ID != second.ID {
		t.Errorf("the draft filter found %d vouchers, want the one draft", drafts.Total)
	}

	// A typed % is a percent sign, not "show me everything" — the same rule the
	// customer search already holds to.
	wildcard, err := svc.List(ctx, Query{Text: "%"})
	if err != nil {
		t.Fatalf("list with a percent sign: %v", err)
	}
	if wildcard.Total != 0 {
		t.Errorf("searching %% found %d vouchers, want none — it is a literal", wildcard.Total)
	}
}
