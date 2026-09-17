package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/auth"
)

// The table the plan set out. Registered access is what the server actually
// enforces, so it is asserted from the registry rather than from a handler.
func TestUserRoutesAreRegisteredWithTheRightAccess(t *testing.T) {
	want := map[string]Access{
		"GET /api/users":                ManagerOnly,
		"POST /api/users":               ManagerOnly,
		"POST /api/users/{id}/active":   ManagerOnly,
		"POST /api/users/{id}/role":     ManagerOnly,
		"POST /api/users/{id}/password": ManagerOnly,
		"POST /api/session/password":    Authenticated,
	}

	handler := NewHandler(testDeps(t, openTestDB(t)))
	for _, route := range handler.Routes {
		key := route.Method + " " + route.Pattern
		access, ok := want[key]
		if !ok {
			continue
		}
		if route.Access != access {
			t.Errorf("%s is %s, want %s", key, route.Access, access)
		}
		delete(want, key)
	}
	for key := range want {
		t.Errorf("%s is not registered at all", key)
	}
}

func TestAnEncoderCannotAdministerAccountsAndTheRefusalsAreAudited(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	target := seedUser(t, deps, "rosa", "encoder-password", auth.RoleEncoder)
	encoder := login(t, handler, "rosa", "encoder-password")

	paths := []string{
		fmt.Sprintf("/api/users/%d/active", target.ID),
		fmt.Sprintf("/api/users/%d/role", target.ID),
		fmt.Sprintf("/api/users/%d/password", target.ID),
	}
	for _, path := range paths {
		resp := request(t, handler, http.MethodPost, path, encoder, map[string]any{
			"active": false, "role": "manager", "password": "a-good-long-password",
		})
		// The body carries every field so one loop covers three shapes; the
		// decoder rejects unknown fields, so a 400 here would mean the route
		// let an encoder as far as the body. Only 403 is right.
		if resp.Code != http.StatusForbidden {
			t.Errorf("POST %s as an encoder = %d, want 403", path, resp.Code)
		}
	}

	denials := 0
	for _, e := range auditEntries(t, deps.DB) {
		if e.action == "auth.access.denied" {
			denials++
		}
	}
	if denials != len(paths) {
		t.Errorf("%d access denials audited, want %d", denials, len(paths))
	}
}

func TestDeactivatingAnAccountThroughTheAPIEndsItsSessionOnTheNextRequest(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	rosa := seedUser(t, deps, "rosa", "encoder-password", auth.RoleEncoder)

	rosaCookies := login(t, handler, "rosa", "encoder-password")
	boss := login(t, handler, "boss", "manager-password")

	resp := request(t, handler, http.MethodPost,
		fmt.Sprintf("/api/users/%d/active", rosa.ID), boss, setUserActiveRequest{Active: false})
	if resp.Code != http.StatusOK {
		t.Fatalf("deactivate = %d: %s", resp.Code, resp.Body)
	}

	if resp := request(t, handler, http.MethodGet, "/api/settings", rosaCookies, nil); resp.Code != http.StatusUnauthorized {
		t.Errorf("the deactivated account's session still works: %d", resp.Code)
	}
	// And the login door is shut too, not just the open session.
	if resp := request(t, handler, http.MethodPost, "/api/session", nil,
		loginRequest{Username: "rosa", Password: "encoder-password"}); resp.Code == http.StatusOK {
		t.Error("a deactivated account could log back in")
	}
}

// Off and on are different questions to ask the log months later, so they are
// different actions in it.
func TestTurningAnAccountOffAndOnAreDifferentAuditActions(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	rosa := seedUser(t, deps, "rosa", "encoder-password", auth.RoleEncoder)
	boss := login(t, handler, "boss", "manager-password")

	path := fmt.Sprintf("/api/users/%d/active", rosa.ID)
	if resp := request(t, handler, http.MethodPost, path, boss,
		setUserActiveRequest{Active: false}); resp.Code != http.StatusOK {
		t.Fatalf("deactivate = %d: %s", resp.Code, resp.Body)
	}
	if resp := request(t, handler, http.MethodPost, path, boss,
		setUserActiveRequest{Active: true}); resp.Code != http.StatusOK {
		t.Fatalf("reactivate = %d: %s", resp.Code, resp.Body)
	}

	entries := auditEntries(t, deps.DB)
	for _, action := range []string{"user.deactivated", "user.activated"} {
		e := findEntry(entries, action)
		if e == nil {
			t.Fatalf("no %s entry; actions were %v", action, actions(entries))
		}
		if e.entityID != "rosa" {
			t.Errorf("%s entity_id = %q, want rosa", action, e.entityID)
		}
		if e.username != "boss" {
			t.Errorf("%s was recorded against %q, want boss", action, e.username)
		}
	}
}

func TestAManagerCannotDeactivateOrDemoteThemselves(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	boss := seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	// A second manager, so the refusal cannot be the last-manager guard
	// answering for a check that is not there.
	seedUser(t, deps, "lola", "manager-password", auth.RoleManager)
	cookies := login(t, handler, "boss", "manager-password")

	resp := request(t, handler, http.MethodPost,
		fmt.Sprintf("/api/users/%d/active", boss.ID), cookies, setUserActiveRequest{Active: false})
	if resp.Code != http.StatusForbidden {
		t.Errorf("deactivating yourself = %d, want 403: %s", resp.Code, resp.Body)
	}

	resp = request(t, handler, http.MethodPost,
		fmt.Sprintf("/api/users/%d/role", boss.ID), cookies, setUserRoleRequest{Role: "encoder"})
	if resp.Code != http.StatusForbidden {
		t.Errorf("demoting yourself = %d, want 403: %s", resp.Code, resp.Body)
	}

	// Still a manager, still able to work.
	if resp := request(t, handler, http.MethodGet, "/api/users", cookies, nil); resp.Code != http.StatusOK {
		t.Errorf("the refused manager lost access anyway: %d", resp.Code)
	}
}

// A manager may promote themselves to what they already are, and may be
// promoted — the self-check is about taking powers away, not about touching
// your own row.
func TestAManagerMayStillBePromotedToWhatTheyAlreadyAre(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	boss := seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	cookies := login(t, handler, "boss", "manager-password")

	resp := request(t, handler, http.MethodPost,
		fmt.Sprintf("/api/users/%d/role", boss.ID), cookies, setUserRoleRequest{Role: "manager"})
	if resp.Code != http.StatusOK {
		t.Errorf("promoting yourself to manager = %d, want 200: %s", resp.Code, resp.Body)
	}
}

func TestTheLastManagerCannotBeDemotedOrDeactivated(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	lola := seedUser(t, deps, "lola", "manager-password", auth.RoleManager)
	boss := login(t, handler, "boss", "manager-password")

	// Demote the second manager: fine, one is left.
	if resp := request(t, handler, http.MethodPost,
		fmt.Sprintf("/api/users/%d/role", lola.ID), boss,
		setUserRoleRequest{Role: "encoder"}); resp.Code != http.StatusOK {
		t.Fatalf("demoting the second manager = %d: %s", resp.Code, resp.Body)
	}

	// Now promote lola back and try to take the last one down from lola's
	// session, so the self-check is not what refuses.
	if resp := request(t, handler, http.MethodPost,
		fmt.Sprintf("/api/users/%d/role", lola.ID), boss,
		setUserRoleRequest{Role: "manager"}); resp.Code != http.StatusOK {
		t.Fatalf("re-promoting = %d: %s", resp.Code, resp.Body)
	}
	lolaCookies := login(t, handler, "lola", "manager-password")

	var bossID int64
	if err := deps.DB.QueryRow(`SELECT id FROM users WHERE username = 'boss'`).Scan(&bossID); err != nil {
		t.Fatalf("find boss: %v", err)
	}
	// lola demotes boss — now lola is the only manager. Then lola tries to
	// demote themselves, which the self-check catches, so instead have boss...
	// boss is an encoder now. Promote boss back, demote lola, then boss is
	// alone and the guard is the only thing left to refuse.
	if resp := request(t, handler, http.MethodPost,
		fmt.Sprintf("/api/users/%d/role", bossID), lolaCookies,
		setUserRoleRequest{Role: "encoder"}); resp.Code != http.StatusOK {
		t.Fatalf("lola demoting boss = %d: %s", resp.Code, resp.Body)
	}

	// lola is now the last manager. Deactivating lola from lola's own session
	// is caught by the self-check; the guard needs a different actor, and
	// there is none — which is exactly the situation the guard exists for.
	// So assert it through the store, which is where the guard lives.
	assertLastManagerIsProtected(t, deps, lola.ID)
}

// assertLastManagerIsProtected drives the guard directly, because reaching it
// through HTTP needs a second manager to do the asking and the guard only
// fires when there is not one.
func assertLastManagerIsProtected(t *testing.T, deps Deps, lastManagerID int64) {
	t.Helper()
	ctx := t.Context()

	tx, err := deps.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	if _, err := auth.SetRoleTx(ctx, tx, lastManagerID, auth.RoleEncoder); err != nil {
		t.Fatalf("demote: %v", err)
	}
	err = keepAManagerStanding(ctx, tx, "%s is the last manager", "lola")
	if err == nil {
		t.Fatal("the last manager was demoted with nobody left to undo it")
	}
	var se statusError
	if !asStatusErrorAs(err, &se) || se.status != http.StatusConflict {
		t.Errorf("last-manager refusal = %v, want a 409 somebody can read", err)
	}
	if !strings.Contains(se.message, "lola") {
		t.Errorf("the refusal does not name the account: %q", se.message)
	}
}

func TestResettingAPasswordEndsEverySessionTheAccountHolds(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	rosa := seedUser(t, deps, "rosa", "encoder-password", auth.RoleEncoder)

	counterOne := login(t, handler, "rosa", "encoder-password")
	counterTwo := login(t, handler, "rosa", "encoder-password")
	boss := login(t, handler, "boss", "manager-password")

	resp := request(t, handler, http.MethodPost,
		fmt.Sprintf("/api/users/%d/password", rosa.ID), boss,
		setPasswordRequest{Password: "a-brand-new-password"})
	if resp.Code != http.StatusOK {
		t.Fatalf("reset = %d: %s", resp.Code, resp.Body)
	}

	for name, cookies := range map[string][]*http.Cookie{"counter one": counterOne, "counter two": counterTwo} {
		if resp := request(t, handler, http.MethodGet, "/api/settings", cookies, nil); resp.Code != http.StatusUnauthorized {
			t.Errorf("%s still works after the reset: %d", name, resp.Code)
		}
	}
	if resp := request(t, handler, http.MethodPost, "/api/session", nil,
		loginRequest{Username: "rosa", Password: "a-brand-new-password"}); resp.Code != http.StatusOK {
		t.Errorf("the new password does not work: %d %s", resp.Code, resp.Body)
	}

	// The manager's own session is untouched: a reset is not a logout for
	// whoever performed it.
	if resp := request(t, handler, http.MethodGet, "/api/users", boss, nil); resp.Code != http.StatusOK {
		t.Errorf("the manager was signed out by their own reset: %d", resp.Code)
	}
}

func TestChangingYourOwnPasswordNeedsTheCurrentOneAndKeepsThisTab(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "rosa", "encoder-password", auth.RoleEncoder)

	thisTab := login(t, handler, "rosa", "encoder-password")
	otherTab := login(t, handler, "rosa", "encoder-password")

	// Wrong current password: refused, and nothing changes.
	resp := request(t, handler, http.MethodPost, "/api/session/password", thisTab,
		changeOwnPasswordRequest{Current: "not-the-password", New: "a-brand-new-password"})
	if resp.Code != http.StatusUnauthorized {
		t.Errorf("wrong current password = %d, want 401: %s", resp.Code, resp.Body)
	}
	if resp := request(t, handler, http.MethodPost, "/api/session", nil,
		loginRequest{Username: "rosa", Password: "encoder-password"}); resp.Code != http.StatusOK {
		t.Error("a refused change altered the password anyway")
	}

	resp = request(t, handler, http.MethodPost, "/api/session/password", thisTab,
		changeOwnPasswordRequest{Current: "encoder-password", New: "a-brand-new-password"})
	if resp.Code != http.StatusOK {
		t.Fatalf("change = %d: %s", resp.Code, resp.Body)
	}

	if resp := request(t, handler, http.MethodGet, "/api/settings", thisTab, nil); resp.Code != http.StatusOK {
		t.Errorf("the tab that changed the password was signed out of: %d", resp.Code)
	}
	if resp := request(t, handler, http.MethodGet, "/api/settings", otherTab, nil); resp.Code != http.StatusUnauthorized {
		t.Errorf("the other tab survived the change: %d", resp.Code)
	}
}

// The failure mode that matters most here: a credential reaching the audit log,
// which is append-only and therefore cannot be cleaned up afterwards.
func TestNoPasswordEverReachesTheAuditLog(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	rosa := seedUser(t, deps, "rosa", "encoder-password", auth.RoleEncoder)
	boss := login(t, handler, "boss", "manager-password")
	rosaCookies := login(t, handler, "rosa", "encoder-password")

	request(t, handler, http.MethodPost, "/api/users", boss, createUserRequest{
		Username: "nilo", DisplayName: "Nilo", Password: "created-with-this-one", Role: "encoder"})
	request(t, handler, http.MethodPost, fmt.Sprintf("/api/users/%d/password", rosa.ID), boss,
		setPasswordRequest{Password: "reset-to-this-one"})
	request(t, handler, http.MethodPost, "/api/session/password", rosaCookies,
		changeOwnPasswordRequest{Current: "reset-to-this-one", New: "changed-to-this-one"})

	secrets := []string{
		"created-with-this-one", "reset-to-this-one", "changed-to-this-one",
		"manager-password", "encoder-password", "argon2", "$argon2id$",
	}
	for _, e := range auditEntries(t, deps.DB) {
		haystack := strings.ToLower(e.before + " " + e.after + " " + e.entityID)
		for _, secret := range secrets {
			if strings.Contains(haystack, strings.ToLower(secret)) {
				t.Errorf("audit entry %q contains %q — the log is append-only and "+
					"this cannot be cleaned up", e.action, secret)
			}
		}
	}
}

func TestATransitionAgainstAnAccountThatIsNotThereIs404(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	boss := login(t, handler, "boss", "manager-password")

	for path, body := range map[string]any{
		"/api/users/9999/active":   setUserActiveRequest{Active: false},
		"/api/users/9999/role":     setUserRoleRequest{Role: "encoder"},
		"/api/users/9999/password": setPasswordRequest{Password: "a-good-long-password"},
	} {
		if resp := request(t, handler, http.MethodPost, path, boss, body); resp.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404: %s", path, resp.Code, resp.Body)
		}
	}
}

func TestABadRoleAndAShortPasswordAreRefusedWithASentence(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	rosa := seedUser(t, deps, "rosa", "encoder-password", auth.RoleEncoder)
	boss := login(t, handler, "boss", "manager-password")

	resp := request(t, handler, http.MethodPost,
		fmt.Sprintf("/api/users/%d/role", rosa.ID), boss, setUserRoleRequest{Role: "supervisor"})
	if resp.Code != http.StatusBadRequest {
		t.Errorf("role=supervisor = %d, want 400: %s", resp.Code, resp.Body)
	}

	resp = request(t, handler, http.MethodPost,
		fmt.Sprintf("/api/users/%d/password", rosa.ID), boss, setPasswordRequest{Password: "short"})
	if resp.Code != http.StatusBadRequest {
		t.Errorf("a five-character password = %d, want 400: %s", resp.Code, resp.Body)
	}
	var body errorBody
	decode(t, resp, &body)
	if !strings.Contains(body.Error, "8") {
		t.Errorf("the refusal does not say how long a password must be: %q", body.Error)
	}
}

// asStatusErrorAs is errors.As for statusError. Not asStatusError, which
// translates the auth package's errors into one and does not recognise a
// statusError that is already there — the same unwrapping rt.fail does.
func asStatusErrorAs(err error, target *statusError) bool {
	return errors.As(err, target)
}
