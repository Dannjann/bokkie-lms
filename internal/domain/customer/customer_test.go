package customer

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
)

// openTestService returns a customer service over a fresh on-disk database.
//
// On disk rather than in memory because that is what the station runs, and
// because the unique index behind the code allocator is one of the things these
// tests exist to prove.
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

	svc := New(database, audit.New(database, log), log)
	svc.now = func() time.Time { return time.Date(2026, 3, 2, 9, 30, 0, 0, time.UTC) }
	return svc, database
}

// create runs CreateTx in its own transaction, which is what a request handler
// does for it in production.
func create(t *testing.T, svc *Service, d Draft) Customer {
	t.Helper()
	c, err := inTx(context.Background(), svc, func(tx *sql.Tx) (Customer, error) {
		return svc.CreateTx(context.Background(), tx, d)
	})
	if err != nil {
		t.Fatalf("create customer %q: %v", d.Name, err)
	}
	return c
}

func inTx[T any](ctx context.Context, svc *Service, fn func(*sql.Tx) (T, error)) (T, error) {
	var zero T

	tx, err := svc.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()

	out, err := fn(tx)
	if err != nil {
		return zero, err
	}
	if err := tx.Commit(); err != nil {
		return zero, err
	}
	return out, nil
}

func TestCodesAreAssignedInSequence(t *testing.T) {
	svc, _ := openTestService(t)

	for i, want := range []string{"BA-0001", "BA-0002", "BA-0003"} {
		got := create(t, svc, Draft{Name: fmt.Sprintf("Farmer %d", i)})
		if got.Code != want {
			t.Errorf("customer %d got code %q, want %q", i, got.Code, want)
		}
	}
}

// The code is the number on the farmer's paper slip. A caller cannot supply one
// because Draft has no field for it — this test is here so that adding one
// later breaks something visible.
func TestADraftCannotCarryACode(t *testing.T) {
	if _, hasCode := any(Draft{}).(interface{ GetCode() string }); hasCode {
		t.Fatal("Draft has grown a code accessor")
	}
	if fields := fmt.Sprintf("%+v", Draft{}); strings.Contains(strings.ToLower(fields), "code") {
		t.Fatalf("Draft has grown a code field: %s", fields)
	}
}

// A deactivated customer must not hand its code to the next one created. This
// is the failure the whole no-delete rule exists to prevent, and it would show
// up as two farmers holding the same code on paper months apart.
func TestADeactivatedCustomerDoesNotReleaseItsCode(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	first := create(t, svc, Draft{Name: "Juan dela Cruz"})

	if _, err := inTx(ctx, svc, func(tx *sql.Tx) (Customer, error) {
		return svc.SetActiveTx(ctx, tx, first.ID, false)
	}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	next := create(t, svc, Draft{Name: "Maria Santos"})
	if next.Code == first.Code {
		t.Fatalf("code %s was reused after a deactivation", next.Code)
	}
	if next.Code != "BA-0002" {
		t.Errorf("got %q, want BA-0002", next.Code)
	}
}

// Allocation happens inside the INSERT precisely so that this cannot produce a
// duplicate. internal/db opens transactions deferred, so a read-then-write
// allocator would let two of these read the same MAX.
func TestConcurrentCreatesGetDistinctCodes(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	const writers = 50

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		codes = map[string]int{}
		fails []error
	)

	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			defer wg.Done()

			c, err := inTx(ctx, svc, func(tx *sql.Tx) (Customer, error) {
				return svc.CreateTx(ctx, tx, Draft{Name: fmt.Sprintf("Farmer %02d", i)})
			})

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fails = append(fails, err)
				return
			}
			codes[c.Code]++
		}(i)
	}
	wg.Wait()

	for _, err := range fails {
		t.Errorf("concurrent create failed: %v", err)
	}
	if len(codes) != writers {
		t.Fatalf("got %d distinct codes from %d writers", len(codes), writers)
	}
	for code, n := range codes {
		if n > 1 {
			t.Errorf("code %s was handed out %d times", code, n)
		}
	}
}

// The end of the sequence must be an error, not a BA-10000 that sorts before
// BA-9999 and quietly breaks the allocator, every listing and every report.
func TestAllocationRefusesPastTheEndOfTheSequence(t *testing.T) {
	svc, database := openTestService(t)
	ctx := context.Background()

	if _, err := database.ExecContext(ctx,
		`INSERT INTO customers (code, name, address, cell_no, active, notes, created_at, updated_at)
		 VALUES ('BA-9999', 'Last Farmer', '', '', 1, '', '2026-03-02T00:00:00Z', '2026-03-02T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seed BA-9999: %v", err)
	}

	_, err := inTx(ctx, svc, func(tx *sql.Tx) (Customer, error) {
		return svc.CreateTx(ctx, tx, Draft{Name: "One Too Many"})
	})
	if !errors.Is(err, ErrCodeSpaceExhausted) {
		t.Fatalf("got %v, want ErrCodeSpaceExhausted", err)
	}

	var count int
	if err := database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM customers WHERE name = 'One Too Many'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("a customer was written past the end of the sequence")
	}
}

// A lower-case code in an import file would slip past the case-sensitive GLOB
// in the allocator and then collide on the NOCASE unique index. It is
// upper-cased on the way in, so the allocator sees it.
func TestImportedCodesAreUpperCasedSoTheAllocatorSeesThem(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	report, err := Plan(strings.NewReader("code,name\nba-0400,Juan dela Cruz\n"), nil)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := svc.Apply(ctx, report, Console); err != nil {
		t.Fatalf("apply: %v", err)
	}

	next := create(t, svc, Draft{Name: "Maria Santos"})
	if next.Code != "BA-0401" {
		t.Fatalf("got %q, want BA-0401 — the allocator did not see the imported code", next.Code)
	}
}

func TestValidationRejectsWhatItShould(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		draft Draft
		field string
	}{
		{"no name", Draft{Name: "   "}, "name"},
		{"name too long", Draft{Name: strings.Repeat("x", maxNameLen+1)}, "name"},
		{"address too long", Draft{Name: "Juan", Address: strings.Repeat("x", maxAddressLen+1)}, "address"},
		{"notes too long", Draft{Name: "Juan", Notes: strings.Repeat("x", maxNotesLen+1)}, "notes"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := inTx(ctx, svc, func(tx *sql.Tx) (Customer, error) {
				return svc.CreateTx(ctx, tx, tc.draft)
			})

			var ve ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("got %v, want a ValidationError", err)
			}
			if ve.Field != tc.field {
				t.Errorf("complaint is about %q, want %q", ve.Field, tc.field)
			}
		})
	}
}

// A number typed one way at one counter and another way at the next is the same
// number, and storing them differently means a search for one never finds the
// other.
func TestCellNumbersAreNormalised(t *testing.T) {
	cases := map[string]string{
		"0917-123-4567":    "09171234567",
		"0917 123 4567":    "09171234567",
		"(0917) 123 4567":  "09171234567",
		"+63 917 123 4567": "+639171234567",
		"":                 "",
		"n/a":              "",
	}

	for in, want := range cases {
		if got := NormaliseCell(in); got != want {
			t.Errorf("NormaliseCell(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUpdateLeavesTheCodeAlone(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	original := create(t, svc, Draft{Name: "Juan dela Cruz", CellNo: "0917-123-4567"})

	updated, err := inTx(ctx, svc, func(tx *sql.Tx) (Customer, error) {
		return svc.UpdateTx(ctx, tx, original.ID, Draft{Name: "Juan dela Cruz Jr", CellNo: "0918 765 4321"})
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if updated.Code != original.Code {
		t.Errorf("code changed from %q to %q", original.Code, updated.Code)
	}
	if updated.Name != "Juan dela Cruz Jr" {
		t.Errorf("name is %q", updated.Name)
	}
	if updated.CellNo != "09187654321" {
		t.Errorf("cell is %q, want the normalised form", updated.CellNo)
	}
}

func TestMissingCustomersAreReported(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	if _, err := svc.ByID(ctx, 404); !errors.Is(err, ErrNotFound) {
		t.Errorf("ByID: got %v, want ErrNotFound", err)
	}

	_, err := inTx(ctx, svc, func(tx *sql.Tx) (Customer, error) {
		return svc.UpdateTx(ctx, tx, 404, Draft{Name: "Nobody"})
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateTx: got %v, want ErrNotFound", err)
	}
}
