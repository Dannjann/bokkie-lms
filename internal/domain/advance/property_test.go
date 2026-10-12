package advance

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

// The example-based tests check the cases somebody thought of. This one
// generates sequences nobody thought of — drafts edited and abandoned, posted,
// voided, interleaved across customers — and asserts the five things that have
// to hold for a voucher to be worth anything:
//
//  1. A posted voucher's lines equal the entries it produced.
//  2. A voided voucher nets to exactly zero for every customer it touched.
//  3. Drafts move no money, however many of them are created, edited or deleted.
//  4. Ref. Nos. are unique, consecutive from refno.start, and never reused.
//  5. The ledger's own first invariant still holds: the cache equals a rebuild.
//
// A counterexample here is a balance a farmer could have disputed.

const (
	propertyCustomers  = 3
	propertyRefNoStart = 1426
)

var propertySeq atomic.Int64

func newPropertyService(t *rapid.T, dir string) (*Service, *ledger.Service, *sql.DB) {
	t.Helper()
	ctx := context.Background()

	path := filepath.Join(dir, fmt.Sprintf("advances-%d.db", propertySeq.Add(1)))
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

	st := station{refNoStart: propertyRefNoStart}
	entries := ledger.New(database, st, audit.New(database, log), log)
	svc := New(database, entries, st, log)
	svc.now = func() time.Time { return time.Date(2026, 3, 2, 9, 30, 0, 0, st.Location()) }
	return svc, entries, database
}

// genDraft draws a voucher that is always valid, so a failure is never just the
// generator producing nonsense.
func genDraft(t *rapid.T, label string) Draft {
	d := Draft{
		Date:  today.AddDays(-rapid.IntRange(0, 60).Draw(t, label+"/day")),
		Notes: "generated",
	}
	for i := 0; i < rapid.IntRange(1, 4).Draw(t, label+"/lines"); i++ {
		line := fmt.Sprintf("%s/line%d", label, i)
		d.Lines = append(d.Lines, DraftLine{
			CustomerID: rapid.Int64Range(1, propertyCustomers).Draw(t, line+"/customer"),
			Amount:     money.FromCentavos(rapid.Int64Range(1, 10_000_000).Draw(t, line+"/amount")),
			Particular: "palay advance",
		})
	}
	return d
}

func TestPropertyVouchersHoldUnderArbitrarySequences(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		svc, entries, database := newPropertyService(rt, dir)

		// The model: what each customer should owe, computed by adding up
		// nothing but the lines of vouchers that are posted and not voided.
		owed := map[int64]int64{}

		var drafts, live []int64 // ids of drafts, and of posted-not-voided vouchers
		nextRefNo := int64(propertyRefNoStart)
		seenRefNos := map[int64]bool{}

		run := func(fn func(tx *sql.Tx) error) error {
			tx, err := database.BeginTx(ctx, nil)
			if err != nil {
				rt.Fatalf("begin: %v", err)
			}
			defer tx.Rollback()

			if err := fn(tx); err != nil {
				return err
			}
			if err := tx.Commit(); err != nil {
				rt.Fatalf("commit: %v", err)
			}
			return nil
		}

		drop := func(ids []int64, id int64) []int64 {
			out := ids[:0]
			for _, candidate := range ids {
				if candidate != id {
					out = append(out, candidate)
				}
			}
			return out
		}

		steps := rapid.IntRange(1, 20).Draw(rt, "steps")
		for step := 0; step < steps; step++ {
			label := fmt.Sprintf("step%d", step)

			switch rapid.SampledFrom([]string{"create", "edit", "delete", "post", "void"}).
				Draw(rt, label+"/action") {

			case "create":
				var v Voucher
				if err := run(func(tx *sql.Tx) error {
					var err error
					v, err = svc.CreateTx(ctx, tx, genDraft(rt, label), dan)
					return err
				}); err != nil {
					rt.Fatalf("create a draft: %v", err)
				}
				if v.RefNo != 0 {
					rt.Fatalf("a draft was numbered %d", v.RefNo)
				}
				drafts = append(drafts, v.ID)

			case "edit":
				if len(drafts) == 0 {
					continue
				}
				id := rapid.SampledFrom(drafts).Draw(rt, label+"/draft")
				if err := run(func(tx *sql.Tx) error {
					_, err := svc.UpdateTx(ctx, tx, id, genDraft(rt, label))
					return err
				}); err != nil {
					rt.Fatalf("edit draft %d: %v", id, err)
				}

			case "delete":
				if len(drafts) == 0 {
					continue
				}
				id := rapid.SampledFrom(drafts).Draw(rt, label+"/draft")
				if err := run(func(tx *sql.Tx) error { return svc.DeleteTx(ctx, tx, id) }); err != nil {
					rt.Fatalf("delete draft %d: %v", id, err)
				}
				drafts = drop(drafts, id)

			case "post":
				if len(drafts) == 0 {
					continue
				}
				id := rapid.SampledFrom(drafts).Draw(rt, label+"/draft")

				var v Voucher
				if err := run(func(tx *sql.Tx) error {
					var err error
					v, err = svc.PostTx(ctx, tx, id, dan)
					return err
				}); err != nil {
					rt.Fatalf("post draft %d: %v", id, err)
				}

				// Invariant 4: consecutive from the setting, and never twice.
				if v.RefNo != nextRefNo {
					rt.Fatalf("voucher %d was numbered %d, want %d", id, v.RefNo, nextRefNo)
				}
				if seenRefNos[v.RefNo] {
					rt.Fatalf("Ref. No. %d was handed out twice", v.RefNo)
				}
				seenRefNos[v.RefNo] = true
				nextRefNo++

				// Invariant 1: the lines and the entries agree.
				assertLinesMatchEntries(rt, svc, v)

				for _, line := range v.Lines {
					owed[line.CustomerID] += line.Amount.Centavos()
				}
				drafts = drop(drafts, id)
				live = append(live, id)

			case "void":
				if len(live) == 0 {
					continue
				}
				id := rapid.SampledFrom(live).Draw(rt, label+"/voucher")

				var v Voucher
				if err := run(func(tx *sql.Tx) error {
					var err error
					v, err = svc.VoidTx(ctx, tx, id, manager, "generated void")
					return err
				}); err != nil {
					rt.Fatalf("void voucher %d: %v", id, err)
				}

				// Invariant 2: everything it did, undone.
				for _, line := range v.Lines {
					owed[line.CustomerID] -= line.Amount.Centavos()
				}
				live = drop(live, id)
			}

			// Invariant 3 is checked by the same comparison as everything else:
			// drafts contribute nothing to `owed`, so any effect they had on a
			// real balance shows up here.
			for id := int64(1); id <= propertyCustomers; id++ {
				b, err := entries.BalanceFor(ctx, id)
				if err != nil {
					rt.Fatalf("balance for customer %d: %v", id, err)
				}
				if got := b.Principal.Centavos(); got != owed[id] {
					rt.Fatalf("after %s, customer %d owes %d centavos, want %d", label, id, got, owed[id])
				}
				if !b.Interest.IsZero() {
					rt.Fatalf("customer %d picked up interest of %s from an advance", id, b.Interest)
				}
			}
		}

		// Invariant 5: the ledger's own first invariant, over a ledger written
		// by real documents rather than by a generator.
		found, err := entries.Discrepancies(ctx)
		if err != nil {
			rt.Fatalf("compare the cache against the ledger: %v", err)
		}
		if len(found) != 0 {
			rt.Fatalf("the cached balances disagree with the entries behind them: %v", found)
		}
	})
}

// assertLinesMatchEntries is invariant 1: every line produced one entry, for
// the same customer, on the same day, for the same amount — and nothing else
// was written.
func assertLinesMatchEntries(t *rapid.T, svc *Service, v Voucher) {
	t.Helper()

	produced, err := svc.Entries(context.Background(), v.ID)
	if err != nil {
		t.Fatalf("read what voucher %d produced: %v", v.ID, err)
	}
	if len(produced) != len(v.Lines) {
		t.Fatalf("voucher %d has %d lines and produced %d entries", v.ID, len(v.Lines), len(produced))
	}

	var lines, entriesTotal int64
	for i, line := range v.Lines {
		e := produced[i]
		if e.CustomerID != line.CustomerID || !e.Date.Equal(line.Date) || e.Principal != line.Amount {
			t.Fatalf("line %d of voucher %d is %d/%s/%s but its entry is %d/%s/%s",
				i+1, v.ID, line.CustomerID, line.Date, line.Amount,
				e.CustomerID, e.Date, e.Principal)
		}
		lines += line.Amount.Centavos()
		entriesTotal += e.Principal.Centavos()
	}
	if lines != entriesTotal || lines != v.Total.Centavos() {
		t.Fatalf("voucher %d totals %s, its lines add to %d and its entries to %d",
			v.ID, v.Total, lines, entriesTotal)
	}
}
