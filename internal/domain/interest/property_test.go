package interest

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	bokkiedb "github.com/Dannjann/bokkie-lms/internal/db"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// The properties that have to hold for an interest run to be worth anything.
//
// The example tests above cover the cases somebody thought of. These generate
// ledgers nobody thought of and charge interest on them. A counterexample here
// is a balance three hundred farmers could have disputed at once.

const propertyCustomers = 3

// runSeq keeps generated databases from colliding on a filename.
var runSeq atomic.Int64

// Charge is pure, so the first three properties need no database at all.
func TestPropertyAChargeIsNeverNegativeAndNeverOverchargges(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		base := money.FromCentavos(rapid.Int64Range(-100_000_000, 100_000_000).Draw(rt, "base"))
		rate := rapid.Int64Range(1, 10_000).Draw(rt, "rate")
		rule := Rule{Method: MethodMonthlyOnBalance, RateBps: rate, Basis: BasisPrincipal}

		charge, err := Charge(base, rule)
		if err != nil {
			rt.Fatalf("charge %d bps on %s: %v", rate, base, err)
		}

		// 1. Nothing is ever charged a negative amount — which would be a
		//    refund dressed up as a charge, and which the ledger would refuse
		//    only after a preview had shown it to somebody.
		if charge.IsNegative() {
			rt.Fatalf("%d bps on %s charged %s", rate, base, charge)
		}

		// 2. At a hundred percent or less, nobody is charged more than the
		//    amount the charge is computed from.
		if base.IsPositive() && charge.Centavos() > base.Centavos() {
			rt.Fatalf("%d bps on %s charged %s, which is more than the base", rate, base, charge)
		}

		// 3. Nothing is charged on nothing.
		if !base.IsPositive() && !charge.IsZero() {
			rt.Fatalf("%d bps on a base of %s charged %s", rate, base, charge)
		}
	})
}

// 4. A bigger debt is never charged less at the same rate. Obvious, and it is
// the property that fails first if rounding is ever applied to something other
// than the exact quotient.
func TestPropertyAChargeRisesWithTheBase(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		smaller := rapid.Int64Range(0, 50_000_000).Draw(rt, "smaller")
		larger := smaller + rapid.Int64Range(0, 50_000_000).Draw(rt, "extra")
		rate := rapid.Int64Range(1, 10_000).Draw(rt, "rate")
		rule := Rule{Method: MethodMonthlyOnBalance, RateBps: rate, Basis: BasisPrincipal}

		low, err := Charge(money.FromCentavos(smaller), rule)
		if err != nil {
			rt.Fatalf("charge on %d: %v", smaller, err)
		}
		high, err := Charge(money.FromCentavos(larger), rule)
		if err != nil {
			rt.Fatalf("charge on %d: %v", larger, err)
		}
		if high.Centavos() < low.Centavos() {
			rt.Fatalf("%d bps charged %s on %d and less — %s — on the larger %d",
				rate, low, smaller, high, larger)
		}
	})
}

// The properties that need a ledger: what a run does to the balances it
// charges, and what voiding it undoes.
func TestPropertyARunChargesExactlyItsLinesAndVoidingPutsThemBack(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		rate := rapid.Int64Range(1, 5_000).Draw(rt, "rate")
		basis := rapid.SampledFrom(Bases).Draw(rt, "basis")
		method := rapid.SampledFrom([]Method{MethodMonthlyOnBalance, MethodFlatOnAdvance}).Draw(rt, "method")

		svc, entries, database := newPropertyRun(rt, dir,
			station{method: method, rateBps: int(rate), basis: basis})

		// A ledger of advances and payments over the period, generated.
		steps := rapid.IntRange(1, 12).Draw(rt, "steps")
		for step := 0; step < steps; step++ {
			label := fmt.Sprintf("step%d", step)
			customer := rapid.Int64Range(1, propertyCustomers).Draw(rt, label+"/customer")
			on := septemberFrom.AddDays(rapid.IntRange(0, 29).Draw(rt, label+"/day"))
			amount := money.FromCentavos(rapid.Int64Range(1, 10_000_000).Draw(rt, label+"/amount"))

			entry := ledger.Entry{CustomerID: customer, Date: on, By: dan}
			if rapid.Bool().Draw(rt, label+"/payment") {
				entry.Type = ledger.TypePayment
				entry.Principal, _ = amount.Neg()
			} else {
				entry.Type = ledger.TypeAdvance
				entry.Principal = amount
			}
			if _, err := entries.Post(ctx, entry); err != nil {
				rt.Fatalf("post a %s: %v", entry.Type, err)
			}
		}

		before := balancesNow(rt, entries)

		var run Run
		err := inTxNoHelper(database, func(tx *sql.Tx) error {
			var err error
			run, err = svc.CreateTx(ctx, tx, september(), dan)
			return err
		})
		if err != nil {
			rt.Fatalf("compute a run: %v", err)
		}
		if len(run.Lines) == 0 {
			return // Nothing owed, nothing charged: a legitimate outcome.
		}

		// 5. The run's total is the sum of what it charges, added up over
		//    money.Money rather than by the database.
		var summed money.Money
		for _, line := range run.Lines {
			var err error
			if summed, err = summed.Add(line.Charge); err != nil {
				rt.Fatalf("total the lines: %v", err)
			}
		}
		if summed != run.Total {
			rt.Fatalf("the run totals %s and its lines add up to %s", run.Total, summed)
		}

		var posted Run
		if err := inTxNoHelper(database, func(tx *sql.Tx) error {
			var err error
			posted, err = svc.PostTx(ctx, tx, run.ID, manager)
			return err
		}); err != nil {
			rt.Fatalf("post the run: %v", err)
		}

		// 6. Every farmer on the run is charged exactly their line, and nobody
		//    else is touched at all.
		after := balancesNow(rt, entries)
		charged := make(map[int64]money.Money, len(posted.Lines))
		for _, line := range posted.Lines {
			charged[line.CustomerID] = line.Charge
		}
		for id := int64(1); id <= propertyCustomers; id++ {
			want, err := before[id].Interest.Add(charged[id])
			if err != nil {
				rt.Fatalf("add up customer %d: %v", id, err)
			}
			if after[id].Interest != want {
				rt.Fatalf("customer %d owes %s of interest after the run, want %s",
					id, after[id].Interest, want)
			}
			// 7. An interest run never moves principal. The ledger refuses an
			//    entry that would, so this failing means the run found another
			//    way to write one.
			if after[id].Principal != before[id].Principal {
				rt.Fatalf("customer %d's principal moved from %s to %s",
					id, before[id].Principal, after[id].Principal)
			}
		}

		// 8. Voiding puts every balance back exactly — not within a centavo,
		//    exactly.
		if err := inTxNoHelper(database, func(tx *sql.Tx) error {
			_, err := svc.VoidTx(ctx, tx, run.ID, manager, "generated void")
			return err
		}); err != nil {
			rt.Fatalf("void the run: %v", err)
		}
		undone := balancesNow(rt, entries)
		for id := int64(1); id <= propertyCustomers; id++ {
			if undone[id].Interest != before[id].Interest || undone[id].Principal != before[id].Principal {
				rt.Fatalf("customer %d owes %s / %s after the void, want %s / %s",
					id, undone[id].Principal, undone[id].Interest,
					before[id].Principal, before[id].Interest)
			}
		}
	})
}

func balancesNow(rt *rapid.T, entries *ledger.Service) map[int64]ledger.Balance {
	rt.Helper()

	balances := make(map[int64]ledger.Balance, propertyCustomers)
	for id := int64(1); id <= propertyCustomers; id++ {
		b, err := entries.BalanceFor(context.Background(), id)
		if err != nil {
			rt.Fatalf("read the balance of customer %d: %v", id, err)
		}
		balances[id] = b
	}
	return balances
}

// newPropertyRun builds an empty database with customers and users in it, and
// an interest service over it at the generated rule.
func newPropertyRun(rt *rapid.T, dir string, st station) (*Service, *ledger.Service, *sql.DB) {
	rt.Helper()
	ctx := context.Background()

	path := filepath.Join(dir, fmt.Sprintf("interest-%d.db", runSeq.Add(1)))
	database, err := bokkiedb.Open(ctx, path)
	if err != nil {
		rt.Fatalf("open database: %v", err)
	}
	rt.Cleanup(func() { database.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := bokkiedb.Migrate(ctx, database, log); err != nil {
		rt.Fatalf("migrate: %v", err)
	}
	for id, name := range map[int64]string{1: "dan", 2: "manager"} {
		if _, err := database.Exec(
			`INSERT INTO users (id, username, display_name, password_hash, role, created_at)
			 VALUES (?, ?, ?, 'x', 'manager', '2025-01-01T00:00:00Z')`, id, name, name); err != nil {
			rt.Fatalf("add user: %v", err)
		}
	}
	for id := int64(1); id <= propertyCustomers; id++ {
		if _, err := database.Exec(
			`INSERT INTO customers (id, code, name, created_at, updated_at)
			 VALUES (?, ?, ?, '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`,
			id, fmt.Sprintf("BA-%04d", id), fmt.Sprintf("Customer %d", id)); err != nil {
			rt.Fatalf("add customer: %v", err)
		}
	}

	entries := ledger.New(database, st, audit.New(database, log), log)
	svc := New(database, entries, st, log)
	svc.now = func() time.Time { return time.Date(2026, 3, 2, 9, 30, 0, 0, st.Location()) }
	return svc, entries, database
}

// inTxNoHelper is inTx without a *testing.T, because rapid's T is a different
// type and these properties still have to run inside the transaction the API
// would have opened.
func inTxNoHelper(database *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
