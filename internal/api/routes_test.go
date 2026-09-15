package api

import (
	"net/http"
	"strings"
	"testing"
)

// Every mutating route must leave a trace. This is checked by enumerating what
// was actually registered rather than by reviewing handlers one at a time,
// because per-handler discipline is exactly what fails silently: some handler
// gets written, nobody remembers the audit call, and there is no way to tell
// which ones are missing without reading all of them.
func TestEveryMutatingRouteIsAudited(t *testing.T) {
	handler := NewHandler(testDeps(t, openTestDB(t)))

	mutating := 0
	for _, route := range handler.Routes {
		if !route.Mutating() {
			continue
		}
		mutating++
		if !route.Audited() {
			t.Errorf("%s %s changes state but writes no audit entry", route.Method, route.Pattern)
		}
	}

	if mutating == 0 {
		t.Fatal("no mutating routes were found; this test is not checking anything")
	}
	t.Logf("%d mutating routes, all audited", mutating)
}

// The self-audited exception has to stay a deliberate, visible choice. A route
// that takes it without saying why is how the exception becomes the rule.
func TestSelfAuditedRoutesExplainThemselves(t *testing.T) {
	handler := NewHandler(testDeps(t, openTestDB(t)))

	for _, route := range handler.Routes {
		if route.audit != auditBySelf {
			continue
		}
		if len(route.why) < 20 {
			t.Errorf("%s %s audits itself but gives no real reason (%q)", route.Method, route.Pattern, route.why)
		}
		t.Logf("%s %s audits itself: %s", route.Method, route.Pattern, route.why)
	}
}

// Wrapper-audited routes must name the action that ends up in the table. An
// empty action would write an entry nobody can search for.
func TestWrapperAuditedRoutesNameTheirAction(t *testing.T) {
	handler := NewHandler(testDeps(t, openTestDB(t)))

	for _, route := range handler.Routes {
		if route.audit != auditByWrapper {
			continue
		}
		if route.Action == "" || route.Entity == "" {
			t.Errorf("%s %s is wrapper-audited but has action %q entity %q",
				route.Method, route.Pattern, route.Action, route.Entity)
		}
		if !strings.Contains(route.Action, ".") {
			t.Errorf("%s %s action %q should read as entity.verb, e.g. setting.changed",
				route.Method, route.Pattern, route.Action)
		}
	}
}

// Public is the dangerous default, so the set of public routes is pinned. A new
// route that is public by accident — or by copying the line above it — fails
// here rather than shipping.
func TestOnlyTheLoginFlowIsPublic(t *testing.T) {
	want := map[string]bool{
		"GET /api/session":  true, // "who am I" — the SPA's first call
		"POST /api/session": true, // log in
		"POST /api/setup":   true, // create the first admin
	}

	handler := NewHandler(testDeps(t, openTestDB(t)))
	for _, route := range handler.Routes {
		key := route.Method + " " + route.Pattern
		if route.Access == Public && !want[key] {
			t.Errorf("%s is public; only the login flow may be", key)
		}
		if route.Access != Public && want[key] {
			t.Errorf("%s is no longer public, but the login flow needs it to be", key)
		}
		delete(want, key)
	}
	for key := range want {
		t.Errorf("%s was expected to be registered and public, but is not registered at all", key)
	}
}

// Every route states an access level because Access is a required argument to
// every registration function — this test documents that and catches a route
// registered straight onto the mux, bypassing the registry.
func TestEveryRegisteredRouteIsUnderTheRegistry(t *testing.T) {
	handler := NewHandler(testDeps(t, openTestDB(t)))

	seen := map[string]bool{}
	for _, route := range handler.Routes {
		key := route.Method + " " + route.Pattern
		if seen[key] {
			t.Errorf("%s is registered twice", key)
		}
		seen[key] = true

		if route.Method == "" || !strings.HasPrefix(route.Pattern, "/api/") {
			t.Errorf("route %+v does not look like an API route", route)
		}
	}

	// A sanity floor: if the registry ever comes back empty, every test above
	// passes vacuously.
	if len(handler.Routes) < 8 {
		t.Errorf("only %d routes registered; the registry looks broken", len(handler.Routes))
	}
}

func TestMutatingIsDecidedByMethod(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if !(Route{Method: method}).Mutating() {
			t.Errorf("%s should count as mutating", method)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		if (Route{Method: method}).Mutating() {
			t.Errorf("%s should not count as mutating", method)
		}
	}
}
