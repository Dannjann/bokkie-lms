package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
)

// Every action this system can write must have a sentence on the audit screen.
//
// The screen falls back to printing the raw action string, so a missing one is
// not a crash — it is a log that reads half-translated, "Account deactivated"
// on one row and `customer.created` on the next. That is exactly the kind of
// fault nobody files a bug about and nobody fixes.
//
// Checked by reading the TypeScript rather than by trusting a reviewer to
// notice, because the thing being tested is that two lists in two languages
// stay in step. The same reasoning as the ledger's no-floats test, which parses
// the syntax tree rather than asking people to remember.
func TestEveryAuditActionHasAWordingOnTheScreen(t *testing.T) {
	named := actionsNamedInTheSPA(t)

	for _, action := range everyActionThisSystemWrites(t) {
		if !named[action] {
			t.Errorf("%q has no wording in web/src/api/audit.ts; the audit screen "+
				"will print the raw string beside actions that read as English", action)
		}
	}
}

// And the other direction: a wording for an action nothing can write is a line
// somebody will keep updating for no reason, and usually a typo.
func TestTheScreenNamesNoActionThisSystemCannotWrite(t *testing.T) {
	writes := map[string]bool{}
	for _, action := range everyActionThisSystemWrites(t) {
		writes[action] = true
	}

	for action := range actionsNamedInTheSPA(t) {
		if !writes[action] {
			t.Errorf("web/src/api/audit.ts gives a wording for %q, which nothing "+
				"in this system writes — a typo, or an action that has been removed", action)
		}
	}
}

// everyActionThisSystemWrites collects the three places an action string can
// come from: the route registry, the audit package's own constants for things
// that happen outside a mutating route, and the one domain constant.
func everyActionThisSystemWrites(t *testing.T) []string {
	t.Helper()

	seen := map[string]bool{}
	add := func(actions ...string) {
		for _, a := range actions {
			if a != "" {
				seen[a] = true
			}
		}
	}

	for _, route := range NewHandler(testDeps(t, openTestDB(t))).Routes {
		add(route.Action)
		add(route.AlsoWrites...)
	}

	// The self-audited routes write these from inside their handlers, so they
	// are not on the registry and have to be listed. They are constants, so a
	// renamed one is a compile error here rather than a silent gap.
	add(
		audit.ActionLoginSucceeded, audit.ActionLoginFailed, audit.ActionLoginBlocked,
		audit.ActionLogout, audit.ActionLock, audit.ActionAccessDenied,
		audit.ActionSetupCompleted, audit.ActionSetupRefused,
		audit.ActionUpdateApplied, audit.ActionUpdateFailed, audit.ActionUpdateRolledBack,
		audit.ActionBackupWritten, audit.ActionLedgerCacheRebuilt,
		customer.ActionImported,
	)

	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	return out
}

// actionNamesKey finds the keys of the actionNames map in the SPA's audit
// client. Quoted keys only, which is how the map is written and how Prettier
// keeps it — a bare key would be a syntax error for a name containing dots.
var actionNamesKey = regexp.MustCompile(`(?m)^\s*'([a-z][a-z0-9_.]*)':`)

func actionsNamedInTheSPA(t *testing.T) map[string]bool {
	t.Helper()

	path := filepath.Join("..", "..", "web", "src", "api", "audit.ts")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	// Only the actionNames map, not the alarming set below it or anything
	// added later — otherwise this test drifts into checking the whole file.
	start := strings.Index(string(source), "const actionNames: Record<string, string> = {")
	if start < 0 {
		t.Fatalf("%s no longer declares actionNames; this test cannot check anything", path)
	}
	rest := string(source)[start:]
	end := strings.Index(rest, "\n}")
	if end < 0 {
		t.Fatalf("%s: actionNames is not closed on its own line", path)
	}

	named := map[string]bool{}
	for _, match := range actionNamesKey.FindAllStringSubmatch(rest[:end], -1) {
		named[match[1]] = true
	}
	if len(named) == 0 {
		t.Fatal("no action wordings were found; the regexp and the file disagree")
	}
	return named
}
