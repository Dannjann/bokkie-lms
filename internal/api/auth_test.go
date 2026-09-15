package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	"github.com/Dannjann/bokkie-lms/internal/auth"
)

// The first-run flow: an empty users table puts the app into setup mode, one
// POST creates the administrator, and that door then closes for good.
func TestFirstRunSetupHappensOnceAndOnlyOnce(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)

	t.Run("an empty install asks for setup", func(t *testing.T) {
		var got sessionResponse
		resp := request(t, handler, http.MethodGet, "/api/session", nil, nil)
		decode(t, resp, &got)

		if !got.NeedsSetup {
			t.Error("needs_setup is false on an empty install; the SPA would show a login form nobody can use")
		}
		if got.User != nil {
			t.Error("a user is reported on an empty install")
		}
	})

	var cookies []*http.Cookie
	t.Run("setup creates a manager and logs them in", func(t *testing.T) {
		resp := request(t, handler, http.MethodPost, "/api/setup", nil, setupRequest{
			Username: "dan", DisplayName: "Dan P", Password: "correct-horse",
		})
		if resp.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", resp.Code, resp.Body)
		}

		var got sessionResponse
		decode(t, resp, &got)
		if got.User == nil || got.User.Role != auth.RoleManager {
			t.Fatalf("setup produced %+v, want a manager", got.User)
		}

		cookies = resp.Result().Cookies()
		if findCookie(cookies, auth.CookieName) == nil {
			t.Error("setup did not start a session; the admin would have to log in again immediately")
		}
	})

	t.Run("setup cannot be run again", func(t *testing.T) {
		resp := request(t, handler, http.MethodPost, "/api/setup", nil, setupRequest{
			Username: "intruder", DisplayName: "", Password: "another-password",
		})
		if resp.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 — the create-admin page must never be reachable twice", resp.Code)
		}

		users, err := deps.Users.All(context.Background())
		if err != nil {
			t.Fatalf("list users: %v", err)
		}
		if len(users) != 1 {
			t.Fatalf("%d users exist after a second setup attempt, want 1", len(users))
		}
	})

	t.Run("the session cookie identifies the admin", func(t *testing.T) {
		var got sessionResponse
		resp := request(t, handler, http.MethodGet, "/api/session", cookies, nil)
		decode(t, resp, &got)

		if got.NeedsSetup {
			t.Error("still reporting needs_setup after an admin exists")
		}
		if got.User == nil || got.User.Username != "dan" {
			t.Fatalf("session reports %+v, want dan", got.User)
		}
	})
}

// The plan's central done-when: two roles behave differently, and the refusal
// is recorded. An encoder hitting a manager-only route must be refused *and*
// leave an audit entry — the request log alone does not say who it was.
func TestEncoderIsRefusedAManagerRouteAndTheRefusalIsAudited(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)

	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	seedUser(t, deps, "clerk", "encoder-password", auth.RoleEncoder)

	encoder := login(t, handler, "clerk", "encoder-password")

	// /api/users is manager-only: who has access to the ledger is a management
	// question.
	resp := request(t, handler, http.MethodGet, "/api/users", encoder, nil)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — an encoder must not read the user roster", resp.Code)
	}

	entries := auditEntries(t, deps.DB)
	denial := findEntry(entries, audit.ActionAccessDenied)
	if denial == nil {
		t.Fatalf("the refusal is not in audit_log; entries were %v", actions(entries))
	}
	if denial.username != "clerk" {
		t.Errorf("refusal recorded against %q, want clerk", denial.username)
	}
	if !strings.Contains(denial.entityID, "/api/users") {
		t.Errorf("refusal entity_id = %q, want it to name the route", denial.entityID)
	}

	// And the manager is not refused, or the test above would pass for the
	// wrong reason.
	manager := login(t, handler, "boss", "manager-password")
	if resp := request(t, handler, http.MethodGet, "/api/users", manager, nil); resp.Code != http.StatusOK {
		t.Errorf("manager got %d on a manager route, want 200: %s", resp.Code, resp.Body)
	}
}

// A mutating route's audit entry must actually land, with before and after, not
// merely be promised by the registry.
func TestAMutationWritesItsBeforeAndAfter(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)

	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	manager := login(t, handler, "boss", "manager-password")

	resp := request(t, handler, http.MethodPut, "/api/settings", manager,
		updateSettingRequest{Key: "backup.retention", Value: "21"})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body)
	}

	entry := findEntry(auditEntries(t, deps.DB), "setting.changed")
	if entry == nil {
		t.Fatal("the settings change left no audit entry")
	}
	if !strings.Contains(entry.before, `"14"`) {
		t.Errorf("before_json = %q, want the old value 14", entry.before)
	}
	if !strings.Contains(entry.after, `"21"`) {
		t.Errorf("after_json = %q, want the new value 21", entry.after)
	}
	if entry.username != "boss" {
		t.Errorf("recorded against %q, want boss", entry.username)
	}
	if entry.requestID == "" {
		t.Error("entry has no request id; it cannot be tied back to a log line")
	}
}

// The setting must take effect immediately. A value that needs a restart is a
// value that is wrong for however long it is until the next upgrade.
func TestAChangedSettingTakesEffectWithoutARestart(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)

	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	manager := login(t, handler, "boss", "manager-password")

	if got := deps.Settings.Location().String(); got != "Asia/Manila" {
		t.Fatalf("starting timezone = %q, want Asia/Manila", got)
	}

	resp := request(t, handler, http.MethodPut, "/api/settings", manager,
		updateSettingRequest{Key: "timezone", Value: "UTC"})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body)
	}

	// Same Store instance, no restart, no reopen.
	if got := deps.Settings.Location().String(); got != "UTC" {
		t.Errorf("timezone is still %q after the change; the cache did not reload", got)
	}
}

func TestUnknownSettingIsRefusedRatherThanStored(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)

	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	manager := login(t, handler, "boss", "manager-password")

	resp := request(t, handler, http.MethodPut, "/api/settings", manager,
		updateSettingRequest{Key: "backup.retenshun", Value: "21"})
	if resp.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 — a misspelled key must not become a row nothing reads", resp.Code)
	}
}

// Repeated wrong passwords must lock the username, and every attempt must be on
// the record.
func TestFailedLoginsAreRateLimitedAndAudited(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "clerk", "encoder-password", auth.RoleEncoder)

	var last *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		last = request(t, handler, http.MethodPost, "/api/session", nil,
			loginRequest{Username: "clerk", Password: "wrong"})
	}

	if last.Code != http.StatusTooManyRequests {
		t.Errorf("status after six failures = %d, want 429", last.Code)
	}
	if !strings.Contains(last.Body.String(), "minute") {
		t.Errorf("lockout message = %q, want it to say how long to wait", last.Body)
	}

	entries := auditEntries(t, deps.DB)
	if findEntry(entries, audit.ActionLoginFailed) == nil {
		t.Errorf("failed logins are not audited; entries were %v", actions(entries))
	}
	if findEntry(entries, audit.ActionLoginBlocked) == nil {
		t.Errorf("the lockout itself is not audited; entries were %v", actions(entries))
	}

	// Even a correct password is refused while locked, or the limit does
	// nothing once the attacker guesses right.
	resp := request(t, handler, http.MethodPost, "/api/session", nil,
		loginRequest{Username: "clerk", Password: "encoder-password"})
	if resp.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 — the lockout must hold even for the right password", resp.Code)
	}
}

// The response must never reveal whether a username exists.
func TestLoginDoesNotRevealWhetherAUsernameExists(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "clerk", "encoder-password", auth.RoleEncoder)

	wrongPassword := request(t, handler, http.MethodPost, "/api/session", nil,
		loginRequest{Username: "clerk", Password: "wrong"})
	noSuchUser := request(t, handler, http.MethodPost, "/api/session", nil,
		loginRequest{Username: "nobody", Password: "wrong"})

	if wrongPassword.Code != noSuchUser.Code {
		t.Errorf("statuses differ: %d vs %d", wrongPassword.Code, noSuchUser.Code)
	}

	var a, b errorBody
	decode(t, wrongPassword, &a)
	decode(t, noSuchUser, &b)
	if a.Error != b.Error {
		t.Errorf("messages differ: %q vs %q — this turns the login form into a username oracle", a.Error, b.Error)
	}

	// The audit log, unlike the response, must record which it was.
	entries := auditEntries(t, deps.DB)
	var reasons []string
	for _, e := range entries {
		if e.action == audit.ActionLoginFailed {
			reasons = append(reasons, e.after)
		}
	}
	if len(reasons) != 2 {
		t.Fatalf("got %d failed-login entries, want 2", len(reasons))
	}
	if reasons[0] == reasons[1] {
		t.Errorf("both failures recorded the same reason %q; the audit log should distinguish them", reasons[0])
	}
}

// Lock ends the session but leaves the username behind, which is what makes a
// long session tolerable without an idle timeout.
func TestLockEndsTheSessionButRemembersWho(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "clerk", "encoder-password", auth.RoleEncoder)

	cookies := login(t, handler, "clerk", "encoder-password")

	resp := request(t, handler, http.MethodPost, "/api/session/lock", cookies, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body)
	}

	locked := findCookie(resp.Result().Cookies(), lockedUserCookie)
	if locked == nil || locked.Value != "clerk" {
		t.Errorf("locked-user cookie = %+v, want clerk so the login form can prefill it", locked)
	}

	// The old cookie must now be dead even if the browser kept a copy.
	after := request(t, handler, http.MethodGet, "/api/settings", cookies, nil)
	if after.Code != http.StatusUnauthorized {
		t.Errorf("status = %d after locking, want 401 — the session was not actually ended", after.Code)
	}

	if findEntry(auditEntries(t, deps.DB), audit.ActionLock) == nil {
		t.Error("the lock is not in audit_log")
	}
}

// Deactivating an account must take effect now, not in thirty days when the
// session would have expired on its own.
func TestDeactivatingAUserEndsTheirSessionImmediately(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "clerk", "encoder-password", auth.RoleEncoder)

	cookies := login(t, handler, "clerk", "encoder-password")
	if resp := request(t, handler, http.MethodGet, "/api/settings", cookies, nil); resp.Code != http.StatusOK {
		t.Fatalf("status = %d before deactivation, want 200", resp.Code)
	}

	if _, err := deps.DB.Exec(`UPDATE users SET active = 0 WHERE username = 'clerk'`); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	if resp := request(t, handler, http.MethodGet, "/api/settings", cookies, nil); resp.Code != http.StatusUnauthorized {
		t.Errorf("status = %d after deactivation, want 401", resp.Code)
	}
}

// --- helpers ---------------------------------------------------------------

func seedUser(t *testing.T, deps Deps, username, password string, role auth.Role) auth.User {
	t.Helper()
	user, err := deps.Users.Create(context.Background(), username, username, password, role)
	if err != nil {
		t.Fatalf("create %s: %v", username, err)
	}
	return user
}

func login(t *testing.T, handler http.Handler, username, password string) []*http.Cookie {
	t.Helper()
	resp := request(t, handler, http.MethodPost, "/api/session", nil,
		loginRequest{Username: username, Password: password})
	if resp.Code != http.StatusOK {
		t.Fatalf("login as %s: status %d: %s", username, resp.Code, resp.Body)
	}
	return resp.Result().Cookies()
}

func request(t *testing.T, handler http.Handler, method, path string, cookies []*http.Cookie, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = strings.NewReader(string(data))
	} else {
		reader = strings.NewReader("")
	}

	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, resp *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(resp.Body.Bytes(), into); err != nil {
		t.Fatalf("decode %q: %v", resp.Body, err)
	}
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

type storedEntry struct {
	action    string
	username  string
	entityID  string
	before    string
	after     string
	requestID string
}

func auditEntries(t *testing.T, database *sql.DB) []storedEntry {
	t.Helper()

	rows, err := database.Query(
		`SELECT action, username, entity_id, coalesce(before_json, ''), coalesce(after_json, ''), request_id
		   FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatalf("read audit_log: %v", err)
	}
	defer rows.Close()

	var entries []storedEntry
	for rows.Next() {
		var e storedEntry
		if err := rows.Scan(&e.action, &e.username, &e.entityID, &e.before, &e.after, &e.requestID); err != nil {
			t.Fatalf("scan audit_log: %v", err)
		}
		entries = append(entries, e)
	}
	return entries
}

func findEntry(entries []storedEntry, action string) *storedEntry {
	for i := range entries {
		if entries[i].action == action {
			return &entries[i]
		}
	}
	return nil
}

func actions(entries []storedEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.action)
	}
	return out
}
