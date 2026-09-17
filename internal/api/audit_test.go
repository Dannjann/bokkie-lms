package api

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/auth"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// auditPage is the shape /api/audit answers with.
type auditPage struct {
	Entries []auditEntryView `json:"entries"`
	Next    int64            `json:"next"`
}

// insertAuditRow writes one entry straight into the table, so a test can put an
// exact timestamp on it. The API cannot: it stamps time.Now().
func insertAuditRow(t *testing.T, deps Deps, at time.Time, userID int64, username, action, entity, entityID string) {
	t.Helper()
	var id any
	if userID != 0 {
		id = userID
	}
	_, err := deps.DB.Exec(
		`INSERT INTO audit_log (at, user_id, username, action, entity, entity_id, request_id, ip)
		 VALUES (?, ?, ?, ?, ?, ?, '', '')`,
		at.UTC().Format(time.RFC3339Nano), id, username, action, entity, entityID)
	if err != nil {
		t.Fatalf("insert audit row: %v", err)
	}
}

func listAudit(t *testing.T, handler http.Handler, cookies []*http.Cookie, query url.Values) auditPage {
	t.Helper()
	resp := request(t, handler, http.MethodGet, "/api/audit?"+query.Encode(), cookies, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/audit?%s = %d: %s", query.Encode(), resp.Code, resp.Body)
	}
	var page auditPage
	decode(t, resp, &page)
	return page
}

func TestAuditIsManagerOnly(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "rosa", "encoder-password", auth.RoleEncoder)
	encoder := login(t, handler, "rosa", "encoder-password")

	for _, path := range []string{"/api/audit", "/api/audit/facets"} {
		if resp := request(t, handler, http.MethodGet, path, encoder, nil); resp.Code != http.StatusForbidden {
			t.Errorf("GET %s as an encoder = %d, want 403", path, resp.Code)
		}
	}
}

// Every filter must return exactly the subset the unfiltered query would, and
// nothing else. Checked against the whole table rather than against a count,
// because a filter that returns the right *number* of wrong rows would pass a
// count.
func TestEachAuditFilterReturnsExactlyTheSubsetTheFullQueryWould(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	boss := seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	rosa := seedUser(t, deps, "rosa", "encoder-password", auth.RoleEncoder)
	cookies := login(t, handler, "boss", "manager-password")

	day := func(d int) time.Time { return time.Date(2026, 3, d, 10, 0, 0, 0, time.UTC) }
	insertAuditRow(t, deps, day(1), boss.ID, "boss", "customer.created", "customer", "BA-0001")
	insertAuditRow(t, deps, day(2), rosa.ID, "rosa", "payment.posted", "payment", "7")
	insertAuditRow(t, deps, day(3), rosa.ID, "rosa", "customer.created", "customer", "BA-0002")
	insertAuditRow(t, deps, day(4), boss.ID, "boss", "setting.changed", "setting", "interest.rate_bps")

	all := listAudit(t, handler, cookies, url.Values{})

	// The subset each filter should produce, computed here from the full page
	// so the expectation cannot drift from the data.
	cases := []struct {
		name  string
		query url.Values
		keep  func(auditEntryView) bool
	}{
		{"by user", url.Values{"user_id": {fmt.Sprint(rosa.ID)}},
			func(e auditEntryView) bool { return e.UserID != nil && *e.UserID == rosa.ID }},
		{"by action", url.Values{"action": {"customer.created"}},
			func(e auditEntryView) bool { return e.Action == "customer.created" }},
		{"by entity", url.Values{"entity": {"customer"}},
			func(e auditEntryView) bool { return e.Entity == "customer" }},
		{"by user and action", url.Values{"user_id": {fmt.Sprint(rosa.ID)}, "action": {"customer.created"}},
			func(e auditEntryView) bool {
				return e.UserID != nil && *e.UserID == rosa.ID && e.Action == "customer.created"
			}},
	}

	for _, tc := range cases {
		want := []string{}
		for _, e := range all.Entries {
			if tc.keep(e) {
				want = append(want, fmt.Sprintf("%d", e.ID))
			}
		}
		got := []string{}
		for _, e := range listAudit(t, handler, cookies, tc.query).Entries {
			got = append(got, fmt.Sprintf("%d", e.ID))
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s returned %v, want %v", tc.name, got, want)
		}
		if len(want) == 0 {
			t.Errorf("%s matches nothing in the fixture; the case proves nothing", tc.name)
		}
	}
}

func TestAuditDatesAreInclusiveAtBothEnds(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	boss := seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	cookies := login(t, handler, "boss", "manager-password")

	// Asia/Manila is UTC+8, so a local day runs 16:00 the previous day to
	// 15:59:59 UTC. These three are chosen to straddle that.
	loc := deps.Settings.Location()
	local := func(d, h int) time.Time { return time.Date(2026, 3, d, h, 0, 0, 0, loc) }
	insertAuditRow(t, deps, local(1, 23), boss.ID, "boss", "a.late-on-the-first", "x", "1")
	insertAuditRow(t, deps, local(2, 12), boss.ID, "boss", "b.middle-of-the-second", "x", "2")
	insertAuditRow(t, deps, local(3, 0), boss.ID, "boss", "c.midnight-on-the-third", "x", "3")
	insertAuditRow(t, deps, local(3, 23), boss.ID, "boss", "d.late-on-the-third", "x", "4")
	insertAuditRow(t, deps, local(4, 1), boss.ID, "boss", "e.on-the-fourth", "x", "5")

	page := listAudit(t, handler, cookies, url.Values{"from": {"2026-03-02"}, "to": {"2026-03-03"}})

	got := map[string]bool{}
	for _, e := range page.Entries {
		got[e.Action] = true
	}
	for _, want := range []string{"b.middle-of-the-second", "c.midnight-on-the-third", "d.late-on-the-third"} {
		if !got[want] {
			t.Errorf("%s is inside 2 March to 3 March and was excluded", want)
		}
	}
	for _, unwanted := range []string{"a.late-on-the-first", "e.on-the-fourth"} {
		if got[unwanted] {
			t.Errorf("%s is outside the period and was included", unwanted)
		}
	}
}

// The reason auditDayBound exists.
//
// `at` is written with RFC3339Nano, which drops trailing zeros from the
// fraction. Compared against a full RFC3339 boundary, '.' sorts before 'Z', so
// an entry a fraction of a second into the range tests as earlier than the
// range's own midnight. This checks the very first instant of a filtered day is
// not silently dropped, whatever its fractional part looks like.
func TestAnEntryInTheFirstMomentOfTheDayIsNotDroppedByTheBoundary(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	boss := seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	cookies := login(t, handler, "boss", "manager-password")
	loc := deps.Settings.Location()

	midnight := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	// Each of these formats differently under RFC3339Nano: no fraction at all,
	// a fraction ending in a zero that is trimmed, and a full nine digits.
	for i, offset := range []time.Duration{0, 123 * time.Millisecond, 999999999 * time.Nanosecond} {
		insertAuditRow(t, deps, midnight.Add(offset), boss.ID, "boss",
			fmt.Sprintf("edge.%d", i), "x", fmt.Sprint(i))
	}

	page := listAudit(t, handler, cookies, url.Values{"from": {"2026-03-02"}, "to": {"2026-03-02"}})
	if len(page.Entries) != 3 {
		actions := make([]string, 0, len(page.Entries))
		for _, e := range page.Entries {
			actions = append(actions, e.Action)
		}
		t.Errorf("the first three instants of 2 March returned %d entries (%v), want 3 — "+
			"the day boundary is comparing against a fractional second", len(page.Entries), actions)
	}
}

// The mutation named in the plan: LIKE instead of =.
//
// The obvious reading of that risk is wrong, and writing the test is what
// showed it. A dot is *literal* in SQL LIKE, so "user.created" as a pattern
// matches only itself and swapping = for LIKE goes unnoticed. What LIKE
// actually leaks is `_` — any single character — and ASCII case-insensitivity.
// So the filter has to be driven from the underscore side to catch anything.
func TestFilteringByActionIsExactNotAPattern(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	boss := seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	cookies := login(t, handler, "boss", "manager-password")
	at := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

	insertAuditRow(t, deps, at, boss.ID, "boss", "user_created", "user", "1")
	insertAuditRow(t, deps, at, boss.ID, "boss", "user.created", "user", "2")
	insertAuditRow(t, deps, at, boss.ID, "boss", "auth.login.failed", "route", "3")

	// The underscore case: as a pattern this matches "user.created" too.
	page := listAudit(t, handler, cookies, url.Values{"action": {"user_created"}})
	if len(page.Entries) != 1 || page.Entries[0].Action != "user_created" {
		got := make([]string, 0, len(page.Entries))
		for _, e := range page.Entries {
			got = append(got, e.Action)
		}
		t.Errorf("filtering on user_created returned %v; the underscore was treated as a wildcard", got)
	}

	// The case-insensitivity case: LIKE ignores ASCII case, = does not.
	page = listAudit(t, handler, cookies, url.Values{"action": {"AUTH.LOGIN.FAILED"}})
	if len(page.Entries) != 0 {
		t.Errorf("filtering on AUTH.LOGIN.FAILED returned %d entries; the match is case-insensitive",
			len(page.Entries))
	}
}

// Paging must not repeat or skip while a filter is on, and must not repeat or
// skip while new entries are arriving either — which is what keyset paging on
// an append-only id buys and OFFSET would not.
func TestPagingWithAFilterNeverRepeatsOrSkipsEvenWhileEntriesArrive(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	boss := seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	other := seedUser(t, deps, "rosa", "encoder-password", auth.RoleEncoder)
	cookies := login(t, handler, "boss", "manager-password")
	at := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

	const wanted = auditPageSize + 30
	for i := range wanted {
		insertAuditRow(t, deps, at, boss.ID, "boss", "customer.created", "customer", fmt.Sprint(i))
		// Noise the filter must exclude, interleaved so a broken filter shows
		// up as a wrong row rather than as a wrong count at the end.
		insertAuditRow(t, deps, at, other.ID, "rosa", "payment.posted", "payment", fmt.Sprint(i))
	}

	query := url.Values{"action": {"customer.created"}, "user_id": {fmt.Sprint(boss.ID)}}
	seen := map[int64]int{}
	total := 0
	cursor := int64(0)

	for range 5 {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		if cursor > 0 {
			q.Set("before", fmt.Sprint(cursor))
		}
		page := listAudit(t, handler, cookies, q)
		for _, e := range page.Entries {
			seen[e.ID]++
			total++
			if e.Action != "customer.created" {
				t.Fatalf("a %s entry came back through a customer.created filter", e.Action)
			}
		}
		// New entries arrive mid-scroll, as they would on a live station.
		insertAuditRow(t, deps, at, boss.ID, "boss", "customer.created", "customer", "late")
		if page.Next == 0 {
			break
		}
		cursor = page.Next
	}

	for id, times := range seen {
		if times > 1 {
			t.Errorf("entry %d was returned %d times", id, times)
		}
	}
	if total < wanted {
		t.Errorf("paged %d entries, want at least the %d that existed before paging began", total, wanted)
	}
}

func TestBadAuditFiltersAreRefusedNamingTheParameter(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	cookies := login(t, handler, "boss", "manager-password")

	for query, wantIn := range map[string]string{
		"user_id=rosa":                  "user_id",
		"before=oldest":                 "before",
		"from=last-tuesday":             "from",
		"to=soon":                       "to",
		"from=2026-03-09&to=2026-03-02": "backwards",
	} {
		resp := request(t, handler, http.MethodGet, "/api/audit?"+query, cookies, nil)
		if resp.Code != http.StatusBadRequest {
			t.Errorf("?%s = %d, want 400", query, resp.Code)
			continue
		}
		var body errorBody
		decode(t, resp, &body)
		if !contains(body.Error, wantIn) {
			t.Errorf("?%s says %q, which does not mention %q", query, body.Error, wantIn)
		}
	}
}

func TestAuditFacetsListWhatIsActuallyInTheTable(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	boss := seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	cookies := login(t, handler, "boss", "manager-password")
	at := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

	insertAuditRow(t, deps, at, boss.ID, "boss", "customer.created", "customer", "BA-0001")
	insertAuditRow(t, deps, at, boss.ID, "boss", "customer.created", "customer", "BA-0002")
	// An entry with no user and no entity — the console writes these.
	insertAuditRow(t, deps, at, 0, "console", "system.backup.written", "", "")

	resp := request(t, handler, http.MethodGet, "/api/audit/facets", cookies, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("facets = %d: %s", resp.Code, resp.Body)
	}
	var facets auditFacets
	decode(t, resp, &facets)

	if countOf(facets.Actions, "customer.created") != 1 {
		t.Errorf("customer.created appears %d times in the action list, want once — "+
			"it is DISTINCT or it is not", countOf(facets.Actions, "customer.created"))
	}
	if countOf(facets.Actions, "system.backup.written") != 1 {
		t.Error("an action written by the console is missing from the facets")
	}
	if countOf(facets.Entities, "") != 0 {
		t.Error("the empty entity is offered as something to filter by")
	}
	// The login that the manager's own session wrote is in there too, which is
	// the point: the list is the table, not a list somebody maintains.
	if countOf(facets.Actions, "auth.login.succeeded") != 1 {
		t.Errorf("the facets miss auth.login.succeeded; actions were %v", facets.Actions)
	}

	found := false
	for _, u := range facets.Users {
		if u.ID == boss.ID && u.Username == "boss" {
			found = true
		}
	}
	if !found {
		t.Errorf("boss is not in the user facets: %+v", facets.Users)
	}
}

func TestAuditTimesAreShownInTheStationsTimezone(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	boss := seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	cookies := login(t, handler, "boss", "manager-password")

	// 16:30 UTC is half past midnight the next day in Manila.
	insertAuditRow(t, deps, time.Date(2026, 3, 2, 16, 30, 0, 0, time.UTC),
		boss.ID, "boss", "z.marker", "x", "1")

	page := listAudit(t, handler, cookies, url.Values{"action": {"z.marker"}})
	if len(page.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(page.Entries))
	}
	want := "2026-03-03T00:30:00+08:00"
	if page.Entries[0].At != want {
		t.Errorf("at = %q, want %q — a log three hours off the wall clock is worse than useless",
			page.Entries[0].At, want)
	}
	_ = settings.Timezone
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func countOf(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want {
			n++
		}
	}
	return n
}
