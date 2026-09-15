package customer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	bokkiedb "github.com/Dannjann/bokkie-lms/internal/db"
)

// The invariant this file exists for is code reuse.
//
// Every other rule in this package can be checked by reading it. Reuse cannot:
// it only appears when a create, a deactivation and an import interleave in an
// order nobody wrote a test for, and its symptom in the world is not an error
// message but two farmers holding slips with the same code, months apart, each
// certain the other is wrong.

// customerSeq keeps generated databases from colliding on a filename.
var customerSeq atomic.Int64

func newPropertyService(t *rapid.T, dir string) (*Service, *sql.DB) {
	t.Helper()
	ctx := context.Background()

	path := filepath.Join(dir, fmt.Sprintf("customers-%d.db", customerSeq.Add(1)))
	database, err := bokkiedb.Open(ctx, path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := bokkiedb.Migrate(ctx, database, log); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := New(database, audit.New(database, log), log)
	svc.now = func() time.Time { return time.Date(2027, 1, 1, 9, 0, 0, 0, time.UTC) }
	return svc, database
}

// TestPropertyCodesAreNeverReused drives arbitrary interleavings of every
// operation that can touch the code space, and checks after each one that the
// three things which must always be true still are.
func TestPropertyCodesAreNeverReused(t *testing.T) {
	dir := t.TempDir()

	rapid.Check(t, func(t *rapid.T) {
		ctx := context.Background()
		svc, database := newPropertyService(t, dir)

		// everIssued is every code this test has ever seen exist. Nothing may
		// ever leave it, and no allocation may ever land back in it.
		everIssued := map[string]bool{}

		// ids of live customer rows, for deactivation to aim at.
		var ids []int64

		steps := rapid.IntRange(1, 40).Draw(t, "steps")
		for step := 0; step < steps; step++ {
			switch rapid.SampledFrom([]string{
				"create", "create", "create", "deactivate", "reactivate", "import",
			}).Draw(t, fmt.Sprintf("op%d", step)) {

			case "create":
				c, err := inTx(ctx, svc, func(tx *sql.Tx) (Customer, error) {
					return svc.CreateTx(ctx, tx, Draft{Name: fmt.Sprintf("Farmer %d", step)})
				})
				if errors.Is(err, ErrCodeSpaceExhausted) {
					continue // a legitimate refusal, not a failure
				}
				if err != nil {
					t.Fatalf("step %d create: %v", step, err)
				}
				if everIssued[c.Code] {
					t.Fatalf("step %d: code %s was issued a second time", step, c.Code)
				}
				everIssued[c.Code] = true
				ids = append(ids, c.ID)

			case "deactivate", "reactivate":
				if len(ids) == 0 {
					continue
				}
				active := rapid.Bool().Draw(t, fmt.Sprintf("active%d", step))
				id := rapid.SampledFrom(ids).Draw(t, fmt.Sprintf("target%d", step))

				if _, err := inTx(ctx, svc, func(tx *sql.Tx) (Customer, error) {
					return svc.SetActiveTx(ctx, tx, id, active)
				}); err != nil {
					t.Fatalf("step %d set active: %v", step, err)
				}

			case "import":
				// Imported codes are chosen freely across the whole space, so
				// they land above, below and exactly on the high-water mark.
				n := rapid.IntRange(1, 3).Draw(t, fmt.Sprintf("rows%d", step))

				var b strings.Builder
				b.WriteString("code,name\n")
				wanted := map[string]bool{}
				for i := 0; i < n; i++ {
					code := fmt.Sprintf("BA-%04d",
						rapid.IntRange(1, 9999).Draw(t, fmt.Sprintf("code%d-%d", step, i)))
					if wanted[code] {
						continue // the file would reject itself; not what this is testing
					}
					wanted[code] = true
					fmt.Fprintf(&b, "%s,Imported %d\n", code, i)
				}
				if len(wanted) == 0 {
					continue
				}

				existing, err := svc.ExistingCodes(ctx)
				if err != nil {
					t.Fatalf("step %d existing codes: %v", step, err)
				}
				rep, err := Plan(strings.NewReader(b.String()), existing)
				if err != nil {
					t.Fatalf("step %d plan: %v", step, err)
				}
				if !rep.OK() {
					// Collisions with codes already on the books are expected
					// and are exactly what the import is supposed to refuse.
					continue
				}
				if _, err := svc.Apply(ctx, rep, Console); err != nil {
					t.Fatalf("step %d apply: %v", step, err)
				}
				for _, row := range rep.rows {
					if everIssued[row.Code] {
						t.Fatalf("step %d: import wrote %s, which already existed", step, row.Code)
					}
					everIssued[row.Code] = true
				}
				ids = append(ids, idsOf(t, database, rep)...)
			}

			assertCodeSpaceIsSound(t, database, everIssued, step)
		}
	})
}

// assertCodeSpaceIsSound checks the three invariants after every operation.
func assertCodeSpaceIsSound(t *rapid.T, database *sql.DB, everIssued map[string]bool, step int) {
	t.Helper()
	ctx := context.Background()

	rows, err := database.QueryContext(ctx, `SELECT code FROM customers ORDER BY code`)
	if err != nil {
		t.Fatalf("step %d read codes: %v", step, err)
	}
	defer rows.Close()

	var present []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			t.Fatalf("step %d scan: %v", step, err)
		}
		present = append(present, code)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("step %d rows: %v", step, err)
	}

	// 1. Every code on the books is distinct.
	seen := map[string]bool{}
	for _, code := range present {
		if seen[code] {
			t.Fatalf("step %d: %s appears twice on the books", step, code)
		}
		seen[code] = true
	}

	// 2. Nothing ever leaves. A deactivated customer is still there, which is
	//    what stops its code being handed to somebody else.
	if len(present) != len(everIssued) {
		t.Fatalf("step %d: %d codes on the books, %d ever issued — something was deleted",
			step, len(present), len(everIssued))
	}
	for code := range everIssued {
		if !seen[code] {
			t.Fatalf("step %d: %s was issued and is no longer on the books", step, code)
		}
	}

	// 3. Codes are stored zero-padded to a fixed width, so lexicographic order
	//    and numeric order agree. This is what lets MAX() be the allocator and
	//    ORDER BY code be a sensible listing; if it ever stops holding, both
	//    break silently.
	numeric := make([]int, len(present))
	for i, code := range present {
		n, err := strconv.Atoi(strings.TrimPrefix(code, codePrefix))
		if err != nil {
			t.Fatalf("step %d: %s is not a well-formed code", step, code)
		}
		numeric[i] = n
	}
	if !sort.IntsAreSorted(numeric) {
		t.Fatalf("step %d: lexicographic order and numeric order disagree: %v", step, present)
	}
}

// idsOf finds the row ids the import just wrote, so later steps can deactivate
// them.
func idsOf(t *rapid.T, database *sql.DB, rep Report) []int64 {
	t.Helper()

	var ids []int64
	for _, row := range rep.rows {
		var id int64
		if err := database.QueryRowContext(context.Background(),
			`SELECT id FROM customers WHERE code = ?`, row.Code).Scan(&id); err != nil {
			t.Fatalf("find imported %s: %v", row.Code, err)
		}
		ids = append(ids, id)
	}
	return ids
}
