package payment

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
// generates sequences nobody thought of — payments drafted, edited, abandoned,
// recorded and voided, interleaved across customers — and asserts what has to
// hold now that recording a payment does not touch the balance:
//
//  1. Nothing this package does writes to the ledger. Applying a payment is a
//     settlement's job (internal/domain/settlement), and how a payment divides
//     between principal and interest is tested there and in TestTheSplit.
//  2. OR Nos. are unique, consecutive from orno.start, and never reused.
//  3. What is waiting to be settled is exactly what has been recorded and not
//     voided.
//  4. A palay payment's amount is the sum of its lines' costs.

const (
	propertyCustomers = 3
	propertyOrNoStart = 4201
)

var propertySeq atomic.Int64

func newPropertyService(t *rapid.T, dir string, rule Rule) (*Service, *ledger.Service, *sql.DB) {
	t.Helper()
	ctx := context.Background()

	path := filepath.Join(dir, fmt.Sprintf("payments-%d.db", propertySeq.Add(1)))
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

	st := station{orNoStart: propertyOrNoStart, applies: rule}
	entries := ledger.New(database, st, audit.New(database, log), log)
	svc := New(database, entries, st, log)
	svc.now = func() time.Time { return time.Date(2026, 3, 2, 9, 30, 0, 0, st.Location()) }
	return svc, entries, database
}

// genDraft draws a payment that is always valid, so a failure is never just the
// generator producing nonsense.
func genDraft(t *rapid.T, label string) Draft {
	d := Draft{
		CustomerID: rapid.Int64Range(1, propertyCustomers).Draw(t, label+"/customer"),
		Date:       today.AddDays(-rapid.IntRange(0, 60).Draw(t, label+"/day")),
		Amount:     money.FromCentavos(rapid.Int64Range(1, 5_000_000).Draw(t, label+"/amount")),
		Method:     rapid.SampledFrom(Methods).Draw(t, label+"/method"),
		Particular: "generated",
	}
	if d.Method == MethodPalay {
		n := rapid.IntRange(1, 4).Draw(t, label+"/lines")
		for i := 0; i < n; i++ {
			d.Lines = append(d.Lines, LineDraft{
				Class: "SLG",
				Sacks: rapid.Int64Range(0, 100).Draw(t, fmt.Sprintf("%s/line%d/sacks", label, i)),
				Kilos: KilosFromTenths(rapid.Int64Range(10, 200_000).Draw(t, fmt.Sprintf("%s/line%d/kilos", label, i))),
				Price: money.FromCentavos(rapid.Int64Range(100, 3_000).Draw(t, fmt.Sprintf("%s/line%d/price", label, i))),
			})
		}
	}
	return d
}

func TestPropertyRecordingPaymentsHoldsUnderArbitrarySequences(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		svc, _, database := newPropertyService(rt, dir, InterestFirst)

		var drafts, recorded []int64
		voided := map[int64]bool{}

		steps := rapid.IntRange(1, 25).Draw(rt, "steps")
		for step := 0; step < steps; step++ {
			label := fmt.Sprintf("step%d", step)
			switch rapid.SampledFrom([]string{"create", "edit", "delete", "record", "void"}).Draw(rt, label+"/action") {
			case "create":
				d := genDraft(rt, label)
				var p Payment
				if err := inTxRapid(rt, database, func(tx *sql.Tx) (err error) {
					p, err = svc.CreateTx(ctx, tx, d, dan)
					return err
				}); err != nil {
					rt.Fatalf("create: %v", err)
				}
				checkLines(rt, p)
				drafts = append(drafts, p.ID)

			case "edit":
				if len(drafts) == 0 {
					continue
				}
				id := rapid.SampledFrom(drafts).Draw(rt, label+"/draft")
				d := genDraft(rt, label)
				var p Payment
				if err := inTxRapid(rt, database, func(tx *sql.Tx) (err error) {
					p, err = svc.UpdateTx(ctx, tx, id, d)
					return err
				}); err != nil {
					rt.Fatalf("edit: %v", err)
				}
				checkLines(rt, p)

			case "delete":
				if len(drafts) == 0 {
					continue
				}
				i := rapid.IntRange(0, len(drafts)-1).Draw(rt, label+"/draft")
				id := drafts[i]
				if err := inTxRapid(rt, database, func(tx *sql.Tx) error { return svc.DeleteTx(ctx, tx, id) }); err != nil {
					rt.Fatalf("delete: %v", err)
				}
				drafts = append(drafts[:i], drafts[i+1:]...)

			case "record":
				if len(drafts) == 0 {
					continue
				}
				i := rapid.IntRange(0, len(drafts)-1).Draw(rt, label+"/draft")
				id := drafts[i]
				if err := inTxRapid(rt, database, func(tx *sql.Tx) error {
					_, err := svc.PostTx(ctx, tx, id, dan)
					return err
				}); err != nil {
					rt.Fatalf("record: %v", err)
				}
				drafts = append(drafts[:i], drafts[i+1:]...)
				recorded = append(recorded, id)

			case "void":
				if len(recorded) == 0 {
					continue
				}
				id := rapid.SampledFrom(recorded).Draw(rt, label+"/payment")
				err := inTxRapid(rt, database, func(tx *sql.Tx) error {
					_, err := svc.VoidTx(ctx, tx, id, manager, "generated")
					return err
				})
				if voided[id] {
					if err == nil {
						rt.Fatalf("payment %d was voided twice", id)
					}
					continue
				}
				if err != nil {
					rt.Fatalf("void: %v", err)
				}
				voided[id] = true
			}
		}

		// 1. Nothing touched the ledger.
		var entries int
		if err := database.QueryRow(`SELECT COUNT(*) FROM ledger_entries`).Scan(&entries); err != nil {
			rt.Fatal(err)
		}
		if entries != 0 {
			rt.Fatalf("recording and voiding payments wrote %d ledger entries", entries)
		}

		// 2. OR Nos. are consecutive from the start, one per recorded payment.
		rows, err := database.Query(`SELECT or_no FROM payments WHERE or_no IS NOT NULL ORDER BY or_no`)
		if err != nil {
			rt.Fatal(err)
		}
		want := int64(propertyOrNoStart)
		for rows.Next() {
			var n int64
			if err := rows.Scan(&n); err != nil {
				rt.Fatal(err)
			}
			if n != want {
				rt.Fatalf("OR No. %d where %d was next", n, want)
			}
			want++
		}
		rows.Close()
		if got := want - propertyOrNoStart; got != int64(len(recorded)) {
			rt.Fatalf("%d OR Nos. for %d recorded payments", got, len(recorded))
		}

		// 3. Waiting to be settled is recorded and not voided.
		waiting := 0
		for id := int64(1); id <= propertyCustomers; id++ {
			ps, err := svc.Unsettled(ctx, id, today)
			if err != nil {
				rt.Fatal(err)
			}
			for _, p := range ps {
				if voided[p.ID] || p.Status != StatusPosted {
					rt.Fatalf("payment %d is %s and waiting to be settled", p.ID, p.Status)
				}
				checkLines(rt, p)
			}
			waiting += len(ps)
		}
		if want := len(recorded) - len(voided); waiting != want {
			rt.Fatalf("%d payments are waiting, want the %d recorded and not voided", waiting, want)
		}
	})
}

// checkLines asserts property 4.
func checkLines(rt *rapid.T, p Payment) {
	if p.Method != MethodPalay {
		if len(p.Lines) != 0 {
			rt.Fatalf("a %s payment has %d lines", p.Method, len(p.Lines))
		}
		return
	}
	total := money.Zero
	for _, l := range p.Lines {
		cost, err := Cost(l.Kilos, l.Price)
		if err != nil || cost != l.Cost {
			rt.Fatalf("a line of %s kg at %s is stored at %s", l.Kilos, l.Price, l.Cost)
		}
		total, _ = total.Add(l.Cost)
	}
	if total != p.Amount {
		rt.Fatalf("palay payment %d is for %s and its lines add up to %s", p.ID, p.Amount, total)
	}
}

func inTxRapid(rt *rapid.T, database *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		rt.Fatalf("begin: %v", err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

var _ = time.Now
