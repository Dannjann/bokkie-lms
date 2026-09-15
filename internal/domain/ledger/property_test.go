package ledger

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
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// These are the most valuable tests in the repository.
//
// The example-based tests above check the cases somebody thought of. These
// generate thousands of sequences nobody thought of and assert the handful of
// properties that have to hold for the ledger to be worth anything: that the
// cache always agrees with the entries, that undoing something puts the balance
// back exactly, that nothing leaks or appears, and that a failed batch leaves
// no trace. A counterexample here is a balance a farmer could have disputed.

// propertyCustomers is how many customers each generated ledger has. Three is
// enough for entries to interleave across customers — which is where a cache
// keyed by the wrong id would show up — and small enough that a shrunk
// counterexample is still readable.
const propertyCustomers = 3

// ledgerSeq keeps generated databases from colliding on a filename.
var ledgerSeq atomic.Int64

// newPropertyLedger builds an empty ledger with customers and users in it.
func newPropertyLedger(t *rapid.T, dir string) (*Service, *sql.DB) {
	t.Helper()
	ctx := context.Background()

	path := filepath.Join(dir, fmt.Sprintf("ledger-%d.db", ledgerSeq.Add(1)))
	database, err := bokkiedb.Open(ctx, path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := bokkiedb.Migrate(ctx, database, log); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for id, name := range map[int64]string{1: "dan", 2: "manager"} {
		if _, err := database.Exec(
			`INSERT INTO users (id, username, display_name, password_hash, role, created_at)
			 VALUES (?, ?, ?, 'x', 'manager', '2026-01-01T00:00:00Z')`, id, name, name); err != nil {
			t.Fatalf("add user: %v", err)
		}
	}
	for id := int64(1); id <= propertyCustomers; id++ {
		if _, err := database.Exec(
			`INSERT INTO customers (id, code, name, created_at, updated_at)
			 VALUES (?, ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
			id, fmt.Sprintf("BA-%04d", id), fmt.Sprintf("Customer %d", id)); err != nil {
			t.Fatalf("add customer: %v", err)
		}
	}

	svc := New(database, station{}, audit.New(database, log), log)
	svc.now = func() time.Time { return time.Date(2027, 1, 1, 9, 0, 0, 0, svc.station.Location()) }
	return svc, database
}

// genEntry draws an entry that is always valid, so that a failure is never just
// the generator producing nonsense.
//
// Amounts are capped at a hundred thousand pesos and sequences at a few dozen
// entries: large enough to be realistic for this station, small enough that the
// running totals cannot approach the int64 limit. Overflow is tested directly
// in the money package, where it can be aimed at precisely.
func genEntry(t *rapid.T, label string) Entry {
	amount := func(name string) money.Money {
		return money.FromCentavos(rapid.Int64Range(1, 10_000_000).Draw(t, label+"/"+name))
	}

	entry := Entry{
		CustomerID: rapid.Int64Range(1, propertyCustomers).Draw(t, label+"/customer"),
		Date: MustDate(2026, time.January, 1).
			AddDays(rapid.IntRange(0, 364).Draw(t, label+"/day")),
		By: Actor{
			UserID:   rapid.Int64Range(1, 2).Draw(t, label+"/user"),
			Username: "generated",
		},
	}

	switch rapid.SampledFrom([]EntryType{
		TypeOpening, TypeAdvance, TypeInterest, TypePayment, TypeAdjustment,
	}).Draw(t, label+"/type") {
	case TypeAdvance:
		entry.Type, entry.Principal = TypeAdvance, amount("principal")
	case TypeInterest:
		entry.Type, entry.Interest = TypeInterest, amount("interest")
	case TypePayment:
		entry.Type = TypePayment
		entry.Principal, _ = amount("principal").Neg()
		entry.Interest, _ = amount("interest").Neg()
	case TypeAdjustment:
		entry.Type, entry.Reason = TypeAdjustment, "generated correction"
		entry.Principal, entry.Interest = amount("principal"), amount("interest")
		if rapid.Bool().Draw(t, label+"/negative-adjustment") {
			entry.Principal, _ = entry.Principal.Neg()
			entry.Interest, _ = entry.Interest.Neg()
		}
	default:
		entry.Type = TypeOpening
		entry.Principal, entry.Interest = amount("principal"), amount("interest")
	}

	// A username has to be real to satisfy the schema's foreign key.
	if entry.By.UserID == 1 {
		entry.By.Username = "dan"
	} else {
		entry.By.Username = "manager"
	}
	return entry
}

// model is the balance as a plain Go map, kept alongside the real ledger. The
// whole point of a property test is that the two are computed by completely
// different means and must still agree.
type model struct {
	principal map[int64]int64
	interest  map[int64]int64

	// postedPrincipal and reversedPrincipal are the conservation ledger: every
	// centavo that was ever posted, and every centavo that was subsequently
	// undone.
	postedPrincipal, postedInterest     int64
	reversedPrincipal, reversedInterest int64
}

func newModel() *model {
	return &model{principal: map[int64]int64{}, interest: map[int64]int64{}}
}

func (m *model) apply(e Entry) {
	m.principal[e.CustomerID] += e.Principal.Centavos()
	m.interest[e.CustomerID] += e.Interest.Centavos()
	if e.IsReversal() {
		m.reversedPrincipal -= e.Principal.Centavos()
		m.reversedInterest -= e.Interest.Centavos()
		return
	}
	m.postedPrincipal += e.Principal.Centavos()
	m.postedInterest += e.Interest.Centavos()
}

// TestPropertyLedgerHoldsUnderArbitrarySequences covers invariants 1, 2, 4 and 5 over
// the same generated history, because they are properties of one ledger rather
// than of one operation and the interesting failures need several operations to
// arrive.
func TestPropertyLedgerHoldsUnderArbitrarySequences(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		svc, _ := newPropertyLedger(rt, dir)
		expected := newModel()

		// reversible holds the ids that can still be undone: everything posted,
		// minus reversals themselves, minus anything already reversed.
		var reversible []int64
		var reversed []int64

		steps := rapid.IntRange(1, 25).Draw(rt, "steps")
		for step := 0; step < steps; step++ {
			label := fmt.Sprintf("step%d", step)

			switch rapid.SampledFrom([]string{"post", "batch", "reverse"}).Draw(rt, label+"/action") {
			case "post":
				entry := genEntry(rt, label)
				id, err := svc.Post(ctx, entry)
				if err != nil {
					rt.Fatalf("post %s for customer %d: %v", entry.Type, entry.CustomerID, err)
				}
				expected.apply(entry)
				reversible = append(reversible, id)

			case "batch":
				count := rapid.IntRange(1, 4).Draw(rt, label+"/lines")
				lines := make([]Entry, count)
				for i := range lines {
					lines[i] = genEntry(rt, fmt.Sprintf("%s/line%d", label, i))
				}
				ids, err := svc.PostBatch(ctx, lines)
				if err != nil {
					rt.Fatalf("post a batch of %d: %v", count, err)
				}
				for i, line := range lines {
					expected.apply(line)
					reversible = append(reversible, ids[i])
				}

			case "reverse":
				if len(reversible) == 0 {
					continue
				}
				at := rapid.IntRange(0, len(reversible)-1).Draw(rt, label+"/target")
				target := reversible[at]
				reversible = append(reversible[:at], reversible[at+1:]...)

				before, err := svc.EntryByID(ctx, target)
				if err != nil {
					rt.Fatalf("read entry %d: %v", target, err)
				}
				balanceBefore := mustBalance(rt, svc, before.CustomerID)

				if _, err := svc.Reverse(ctx, target, Actor{UserID: 2, Username: "manager"}, "generated void"); err != nil {
					rt.Fatalf("reverse entry %d: %v", target, err)
				}
				reversed = append(reversed, target)

				// Invariant 2: a reversal puts the balance back to exactly what
				// it was before the entry it undoes contributed anything — not
				// within a centavo, exactly.
				wantPrincipal, _ := balanceBefore.Principal.Sub(before.Principal)
				wantInterest, _ := balanceBefore.Interest.Sub(before.Interest)
				after := mustBalance(rt, svc, before.CustomerID)
				if after.Principal != wantPrincipal || after.Interest != wantInterest {
					rt.Fatalf("reversing entry %d left customer %d at %s/%s, want %s/%s",
						target, before.CustomerID, after.Principal, after.Interest, wantPrincipal, wantInterest)
				}

				expected.apply(Entry{
					CustomerID: before.CustomerID,
					Type:       TypeReversal,
					Principal:  mustNeg(rt, before.Principal),
					Interest:   mustNeg(rt, before.Interest),
				})
			}
		}

		// Invariant 5: nothing already undone can be undone again, and the
		// refusal changes nothing.
		for _, target := range reversed {
			before := mustBalance(rt, svc, mustEntry(rt, svc, target).CustomerID)
			if _, err := svc.Reverse(ctx, target, Actor{UserID: 2, Username: "manager"}, "again"); err == nil {
				rt.Fatalf("entry %d was reversed twice", target)
			}
			if after := mustBalance(rt, svc, mustEntry(rt, svc, target).CustomerID); after != before {
				rt.Fatalf("a refused reversal of entry %d moved the balance from %s to %s",
					target, before.Principal, after.Principal)
			}
		}

		// Invariant 1, first half: every cached balance agrees with the sum of
		// the entries behind it.
		if found, err := svc.Discrepancies(ctx); err != nil {
			rt.Fatalf("compare the cache against the ledger: %v", err)
		} else if len(found) != 0 {
			rt.Fatalf("the cache disagreed with the ledger: %v", found)
		}

		// ... and against a model computed by entirely different means.
		var sumPrincipal, sumInterest int64
		for id := int64(1); id <= propertyCustomers; id++ {
			got := mustBalance(rt, svc, id)
			if got.Principal.Centavos() != expected.principal[id] ||
				got.Interest.Centavos() != expected.interest[id] {
				rt.Fatalf("customer %d holds %s/%s, the model says %d/%d centavos",
					id, got.Principal, got.Interest, expected.principal[id], expected.interest[id])
			}
			sumPrincipal += got.Principal.Centavos()
			sumInterest += got.Interest.Centavos()
		}

		// Invariant 4: conservation. Nothing appears and nothing leaks — the
		// balances add up to everything ever posted, less everything undone.
		if want := expected.postedPrincipal - expected.reversedPrincipal; sumPrincipal != want {
			rt.Fatalf("principal across all customers is %d centavos, want %d posted less reversed",
				sumPrincipal, want)
		}
		if want := expected.postedInterest - expected.reversedInterest; sumInterest != want {
			rt.Fatalf("interest across all customers is %d centavos, want %d posted less reversed",
				sumInterest, want)
		}

		// Invariant 1, second half: throwing the cache away and rebuilding it
		// from scratch changes nothing. This is the one that catches nearly
		// everything, because it is the only check that does not trust the
		// incremental path at all.
		cached := map[int64]Balance{}
		for id := int64(1); id <= propertyCustomers; id++ {
			cached[id] = mustBalance(rt, svc, id)
		}
		if _, err := svc.RebuildCache(ctx); err != nil {
			rt.Fatalf("rebuild the cache: %v", err)
		}
		for id := int64(1); id <= propertyCustomers; id++ {
			if got := mustBalance(rt, svc, id); got != cached[id] {
				rt.Fatalf("customer %d was %s/%s before the rebuild and %s/%s after",
					id, cached[id].Principal, cached[id].Interest, got.Principal, got.Interest)
			}
		}
	})
}

// TestPropertyOrderOfPostingDoesNotChangeTheBalance is invariant 3.
//
// It matters because the order entries arrive in is not something the system
// controls: a voucher dated Monday can be typed on Wednesday, after Tuesday's
// payment has already been entered. A balance that depended on typing order
// would be a balance that changed when somebody caught up on their backlog.
func TestPropertyOrderOfPostingDoesNotChangeTheBalance(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		count := rapid.IntRange(1, 15).Draw(rt, "entries")
		entries := make([]Entry, count)
		for i := range entries {
			entries[i] = genEntry(rt, fmt.Sprintf("entry%d", i))
		}

		order := rapid.Permutation(entries).Draw(rt, "shuffled")

		first, _ := newPropertyLedger(rt, dir)
		second, _ := newPropertyLedger(rt, dir)
		for _, e := range entries {
			if _, err := first.Post(ctx, e); err != nil {
				rt.Fatalf("post in the given order: %v", err)
			}
		}
		for _, e := range order {
			if _, err := second.Post(ctx, e); err != nil {
				rt.Fatalf("post in the shuffled order: %v", err)
			}
		}

		for id := int64(1); id <= propertyCustomers; id++ {
			a, b := mustBalance(rt, first, id), mustBalance(rt, second, id)
			if a != b {
				rt.Fatalf("customer %d is %s/%s in one order and %s/%s in another",
					id, a.Principal, a.Interest, b.Principal, b.Interest)
			}
		}
	})
}

// TestPropertyAFailedBatchLeavesNothingBehind is invariant 6.
//
// Half a posted voucher is not a partial success. It is a corruption in a table
// that cannot be edited, which means somebody would have to correct it with
// reversing entries that do not correspond to anything that ever happened.
func TestPropertyAFailedBatchLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		svc, database := newPropertyLedger(rt, dir)

		// Some history first, so the test proves the batch left nothing rather
		// than that the ledger happened to be empty.
		existing := rapid.IntRange(0, 5).Draw(rt, "existing")
		for i := 0; i < existing; i++ {
			if _, err := svc.Post(ctx, genEntry(rt, fmt.Sprintf("existing%d", i))); err != nil {
				rt.Fatalf("seed the ledger: %v", err)
			}
		}
		before := snapshot(rt, svc)

		count := rapid.IntRange(1, 6).Draw(rt, "lines")
		lines := make([]Entry, count)
		for i := range lines {
			lines[i] = genEntry(rt, fmt.Sprintf("line%d", i))
		}

		// One line is poisoned, at a position the generator chooses. Naming a
		// customer that does not exist fails on the foreign key, which is a
		// failure the database raises part-way through the transaction rather
		// than one Validate could have caught up front.
		poisoned := rapid.IntRange(0, count-1).Draw(rt, "poisoned")
		lines[poisoned].CustomerID = 9999

		if _, err := svc.PostBatch(ctx, lines); err == nil {
			rt.Fatalf("a batch with an impossible line at %d of %d was posted", poisoned, count)
		}

		var total int
		if err := database.QueryRow(`SELECT COUNT(*) FROM ledger_entries`).Scan(&total); err != nil {
			rt.Fatalf("count entries: %v", err)
		}
		if total != existing {
			rt.Fatalf("the ledger holds %d entries after a failed batch, want the %d it started with",
				total, existing)
		}
		if after := snapshot(rt, svc); after != before {
			rt.Fatalf("a failed batch moved the balances from %v to %v", before, after)
		}
	})
}

// snapshot is every customer's balance in a form that compares with ==.
func snapshot(t *rapid.T, svc *Service) [propertyCustomers]Balance {
	t.Helper()
	var all [propertyCustomers]Balance
	for id := int64(1); id <= propertyCustomers; id++ {
		all[id-1] = mustBalance(t, svc, id)
	}
	return all
}

func mustBalance(t *rapid.T, svc *Service, customerID int64) Balance {
	t.Helper()
	b, err := svc.BalanceFor(context.Background(), customerID)
	if err != nil {
		t.Fatalf("balance for customer %d: %v", customerID, err)
	}
	return b
}

func mustEntry(t *rapid.T, svc *Service, id int64) Entry {
	t.Helper()
	e, err := svc.EntryByID(context.Background(), id)
	if err != nil {
		t.Fatalf("read entry %d: %v", id, err)
	}
	return e
}

func mustNeg(t *rapid.T, m money.Money) money.Money {
	t.Helper()
	negated, err := m.Neg()
	if err != nil {
		t.Fatalf("negate %s: %v", m, err)
	}
	return negated
}
