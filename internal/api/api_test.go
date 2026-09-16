package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	"github.com/Dannjann/bokkie-lms/internal/auth"
	"github.com/Dannjann/bokkie-lms/internal/backup"
	bokkiedb "github.com/Dannjann/bokkie-lms/internal/db"
	"github.com/Dannjann/bokkie-lms/internal/domain/advance"
	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/payment"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

func TestHealthzReportsBuildAndDatabase(t *testing.T) {
	srv := httptest.NewServer(NewHandler(testDeps(t, openTestDB(t))))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get(RequestIDHeader) == "" {
		t.Errorf("missing %s header — a screenshot must be enough to find the log lines", RequestIDHeader)
	}

	var body healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ok" || body.Database != "ok" {
		t.Errorf("body = %+v, want status and database ok", body)
	}
	if body.Version == "" {
		t.Error("version is empty; the first support question is which build is running")
	}
}

// A server that answers HTTP while the database is unreachable is down, and
// saying "ok" would hide that from whoever is polling.
func TestHealthzIsDegradedWhenDatabaseIsClosed(t *testing.T) {
	database := openTestDB(t)
	handler := NewHandler(testDeps(t, database))

	// Closed after the handler is built, which is the real-world shape of this
	// failure: the app started fine and the database went away underneath it.
	database.Close()

	resp := do(t, handler, http.MethodGet, "/healthz")

	if resp.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "unreachable") {
		t.Errorf("body = %q, want it to name the database as the problem", resp.Body)
	}
}

func TestUnknownAPIPathReturnsJSONNotHTML(t *testing.T) {
	resp := do(t, NewHandler(testDeps(t, openTestDB(t))), http.MethodGet, "/api/no-such-thing")

	if resp.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.Code)
	}
	if ct := resp.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content type = %q, want JSON — an API that answers HTML is baffling to debug", ct)
	}
	var body errorBody
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.RequestID == "" {
		t.Error("error body has no request ID")
	}
}

// One bad request must never take the process down: the station is mid-shift.
func TestPanicBecomesA500(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := withRequestID(withRequestLog(log, withPanicRecovery(log,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("boom")
		}))))

	resp := do(t, handler, http.MethodGet, "/")

	if resp.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.Code)
	}
	if strings.Contains(resp.Body.String(), "boom") {
		t.Error("panic detail leaked to the client; it belongs in the log only")
	}
}

func do(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// testDeps builds the full dependency graph against a migrated, temporary
// database. Handlers are exercised through the real stores rather than fakes:
// the interesting bugs in this step are in the SQL and in the middleware
// ordering, and a fake store would test neither.
func testDeps(t *testing.T, database *sql.DB) Deps {
	t.Helper()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := settings.Open(context.Background(), database)
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}

	folder := filepath.Join(t.TempDir(), "backups")
	auditLog := audit.New(database, log)
	ledgerSvc := ledger.New(database, store, auditLog, log)
	return Deps{
		Logger:       log,
		DB:           database,
		Started:      time.Now().Add(-time.Minute),
		Customers:    customer.New(database, auditLog, log),
		Ledger:       ledgerSvc,
		Advances:     advance.New(database, ledgerSvc, store, log),
		Payments:     payment.New(database, ledgerSvc, store, log),
		Users:        auth.NewUsers(database),
		Sessions:     auth.NewSessions(database),
		Limiter:      auth.NewLimiter(),
		Audit:        auditLog,
		Settings:     store,
		Backup:       backup.NewScheduler(database, store, folder, log),
		BackupFolder: folder,
	}
}

// openTestDB opens a migrated database in a temporary directory. It is a real
// file rather than :memory: because WAL, the pragmas db.Open verifies, and
// VACUUM INTO all behave differently in memory — and those are precisely what
// the backup tests are about.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := bokkiedb.Open(context.Background(), filepath.Join(t.TempDir(), "bokkie.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := bokkiedb.Migrate(context.Background(), database,
		slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}

// Deep links and refreshes must work: an unknown path is the SPA router's
// business, not a 404. The exception is /api, which has to stay JSON.
func TestSPAServesShellForUnknownPathsButNotForAPI(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	deps.SPA = fstest.MapFS{
		"index.html":          {Data: []byte("<!doctype html><div id=app>")},
		"assets/index-abc.js": {Data: []byte("console.log(1)")},
	}
	handler := NewHandler(deps)

	t.Run("deep link gets the shell", func(t *testing.T) {
		resp := do(t, handler, http.MethodGet, "/customers/BA-0001")
		if resp.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.Code)
		}
		if !strings.Contains(resp.Body.String(), "id=app") {
			t.Errorf("body = %q, want the app shell", resp.Body)
		}
		if cc := resp.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("shell Cache-Control = %q, want no-cache so an upgrade is not pinned to old assets", cc)
		}
	})

	t.Run("hashed asset is served and cached hard", func(t *testing.T) {
		resp := do(t, handler, http.MethodGet, "/assets/index-abc.js")
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.Code)
		}
		if !strings.Contains(resp.Header().Get("Cache-Control"), "immutable") {
			t.Errorf("asset Cache-Control = %q, want immutable", resp.Header().Get("Cache-Control"))
		}
	})

	t.Run("unknown api path is still JSON", func(t *testing.T) {
		resp := do(t, handler, http.MethodGet, "/api/no-such-thing")
		if ct := resp.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("content type = %q, want JSON — the SPA fallback must not swallow /api", ct)
		}
	})
}
