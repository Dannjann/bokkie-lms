package report

import (
	"context"
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

// What has to be true of a report for it to be worth printing.
//
// The example tests above check ledgers somebody worked out by hand. These
// generate ledgers nobody thought of and assert the three things that make a
// page trustworthy: the rows add up to the totals, the buckets add up to what
// is outstanding, and the report agrees with the ledger it was built from. A
// counterexample here is a printed page a farmer could disprove.

const propertyCustomers = 3

var reportSeq atomic.Int64

func newPropertyReports(rt *rapid.T, dir string) (*Service, *ledger.Service) {
	rt.Helper()
	ctx := context.Background()

	path := filepath.Join(dir, fmt.Sprintf("report-%d.db", reportSeq.Add(1)))
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

	entries := ledger.New(database, station{}, audit.New(database, log), log)
	return New(database, entries, log), entries
}

// genLedger fills a ledger with advances, payments and interest over 2026.
func genLedger(rt *rapid.T, entries *ledger.Service) {
	ctx := context.Background()
	steps := rapid.IntRange(1, 20).Draw(rt, "steps")

	for step := 0; step < steps; step++ {
		label := fmt.Sprintf("step%d", step)
		customer := rapid.Int64Range(1, propertyCustomers).Draw(rt, label+"/customer")
		on := ledger.MustDate(2026, time.January, 1).
			AddDays(rapid.IntRange(0, 300).Draw(rt, label+"/day"))
		amount := money.FromCentavos(rapid.Int64Range(1, 5_000_000).Draw(rt, label+"/amount"))

		entry := ledger.Entry{CustomerID: customer, Date: on, By: dan}
		switch rapid.SampledFrom([]string{"advance", "payment", "interest"}).Draw(rt, label+"/kind") {
		case "payment":
			entry.Type = ledger.TypePayment
			entry.Principal, _ = amount.Neg()
		case "interest":
			entry.Type, entry.Interest = ledger.TypeInterest, amount
		default:
			entry.Type, entry.Principal = ledger.TypeAdvance, amount
		}
		if _, err := entries.Post(ctx, entry); err != nil {
			rt.Fatalf("post a %s: %v", entry.Type, err)
		}
	}
}

func TestPropertyReportsAddUpAndAgreeWithTheLedger(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		svc, entries := newPropertyReports(rt, dir)
		genLedger(rt, entries)

		asOf := ledger.MustDate(2026, time.January, 1).
			AddDays(rapid.IntRange(0, 365).Draw(rt, "as-of"))
		from := ledger.MustDate(2026, time.January, 1)

		// 1. Current Advances is the ledger, restated. Every row must be the
		//    balance the ledger reports for that customer, to the centavo.
		current, err := svc.CurrentAdvances(ctx, asOf)
		if err != nil {
			rt.Fatalf("current advances: %v", err)
		}
		var principal, interest money.Money
		for _, row := range current.Rows {
			balance, err := entries.BalanceAt(ctx, row.CustomerID, asOf)
			if err != nil {
				rt.Fatalf("balance: %v", err)
			}
			if row.Principal != balance.Principal || row.Interest != balance.Interest {
				rt.Fatalf("customer %d is on the report at %s / %s and the ledger says %s / %s",
					row.CustomerID, row.Principal, row.Interest, balance.Principal, balance.Interest)
			}
			principal, _ = principal.Add(row.Principal)
			interest, _ = interest.Add(row.Interest)
		}
		if principal != current.Principal || interest != current.Interest {
			rt.Fatalf("the rows sum to %s / %s and the report totals say %s / %s",
				principal, interest, current.Principal, current.Interest)
		}

		// 2. ROI's rows add up to its totals. The ledger generated here has no
		//    settlements, so there are no rows — what this guards is that the
		//    totals are never invented from somewhere other than the rows.
		roi, err := svc.ROI(ctx, from, asOf)
		if err != nil {
			rt.Fatalf("roi: %v", err)
		}
		var repaid, collected, returned money.Money
		for _, row := range roi.Rows {
			repaid, _ = repaid.Add(row.Principal)
			collected, _ = collected.Add(row.Interest)
			returned, _ = returned.Add(row.Amount)
		}
		if repaid != roi.Repaid || collected != roi.InterestCollected || returned != roi.Returned {
			rt.Fatalf("the ROI rows sum to %s / %s / %s and the totals say %s / %s / %s",
				repaid, collected, returned, roi.Repaid, roi.InterestCollected, roi.Returned)
		}

		// 3. Outstanding on the ROI block is the whole book, which is what
		//    Current Advances totals as at the same day.
		if outstanding, err := current.Principal.Add(current.Interest); err != nil {
			rt.Fatalf("total: %v", err)
		} else if outstanding != roi.Outstanding {
			rt.Fatalf("ROI says %s is outstanding and Current Advances totals %s",
				roi.Outstanding, outstanding)
		}

		// 4. The aging buckets sum to the outstanding principal — of the
		//    farmers who have any. A customer in credit contributes nothing to
		//    the buckets and a negative figure to the balance, so the two agree
		//    only over the ones actually in debt.
		aging, err := svc.Aging(ctx, asOf)
		if err != nil {
			rt.Fatalf("aging: %v", err)
		}
		for _, row := range aging.Rows {
			total, err := row.Buckets.Total()
			if err != nil {
				rt.Fatalf("total the buckets: %v", err)
			}
			if total != row.Principal {
				rt.Fatalf("customer %d's buckets sum to %s and the row says %s",
					row.CustomerID, total, row.Principal)
			}

			balance, err := entries.BalanceAt(ctx, row.CustomerID, asOf)
			if err != nil {
				rt.Fatalf("balance: %v", err)
			}
			if balance.Principal.IsPositive() && total != balance.Principal {
				rt.Fatalf("customer %d owes %s of principal and is aged at %s",
					row.CustomerID, balance.Principal, total)
			}
			for _, bucket := range []money.Money{row.Current, row.Days60, row.Days90, row.Over90} {
				if bucket.IsNegative() {
					rt.Fatalf("customer %d has a negative bucket: %s", row.CustomerID, bucket)
				}
			}
		}

	})
}
