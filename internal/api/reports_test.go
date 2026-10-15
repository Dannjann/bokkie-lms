package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/domain/report"
)

// The four reports through the real routes, against a station with money on the
// books: one farmer who borrowed ₱10,000 ninety days ago and has not been
// settled, and one lent ₱1,000 today and settled in full.
func newReportFixture(t *testing.T) *settlementFixture {
	t.Helper()

	f := newSettlementFixture(t)
	day := today(t)

	var settled struct {
		ID   int64  `json:"id"`
		Code string `json:"code"`
	}
	resp := request(t, f.handler, http.MethodPost, "/api/customers", f.encoder,
		map[string]any{"name": "Maria Santos"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("seed a second farmer: status %d: %s", resp.Code, resp.Body)
	}
	decode(t, resp, &settled)

	f.advance(t, settled.ID, day, "1000.00")
	f.deliver(t, settled.ID, day, "100.0", "10.00")
	f.post(t, f.settle(t, map[string]any{"customer_id": settled.ID, "cutoff": day}).ID)
	return f
}

// period is the query string covering everything the fixture did.
func (f *settlementFixture) period(t *testing.T) string {
	return "?from=" + daysAgo(t, 100) + "&to=" + today(t)
}

func TestCurrentAdvancesReportsWhoOwesWhat(t *testing.T) {
	f := newReportFixture(t)

	resp := request(t, f.handler, http.MethodGet, "/api/reports/current-advances", f.manager, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("current advances: status %d: %s", resp.Code, resp.Body)
	}

	var current report.CurrentAdvances
	decode(t, resp, &current)
	if len(current.Rows) != 1 {
		t.Fatalf("the report has %d rows, want the one farmer still owing: %+v", len(current.Rows), current.Rows)
	}
	if current.Rows[0].Code != f.farmer.Code || current.Total.String() != "10000.00" {
		t.Errorf("the report shows %+v totalling %s, want %s at 10000.00",
			current.Rows[0], current.Total, f.farmer.Code)
	}
}

func TestROIReportsThePortfolioAndItsRows(t *testing.T) {
	f := newReportFixture(t)

	resp := request(t, f.handler, http.MethodGet, "/api/reports/roi"+f.period(t), f.manager, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("roi: status %d: %s", resp.Code, resp.Body)
	}

	var roi report.ROI
	decode(t, resp, &roi)
	if roi.Deployed.String() != "11000.00" || roi.Repaid.String() != "1000.00" {
		t.Errorf("deployed %s and repaid %s, want 11000.00 and 1000.00", roi.Deployed, roi.Repaid)
	}
	if roi.Outstanding.String() != "10000.00" {
		t.Errorf("outstanding %s, want 10000.00", roi.Outstanding)
	}
	if roi.Farmers != 1 || roi.Vouchers != 2 {
		t.Errorf("%d farmers settled over %d advance slips, want 1 and 2", roi.Farmers, roi.Vouchers)
	}
	if len(roi.Rows) != 1 || roi.Rows[0].Amount.String() != "1000.00" || roi.Rows[0].SettlementNo != 1 {
		t.Fatalf("the report has rows %+v, want the one settlement returning 1000.00", roi.Rows)
	}
}

func TestAgingReportsTheOutstandingPrincipalByAge(t *testing.T) {
	f := newReportFixture(t)

	resp := request(t, f.handler, http.MethodGet, "/api/reports/aging", f.manager, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("aging: status %d: %s", resp.Code, resp.Body)
	}

	var aging report.Aging
	decode(t, resp, &aging)
	if len(aging.Rows) != 1 {
		t.Fatalf("the report has %d rows, want the one farmer still owing: %+v", len(aging.Rows), aging.Rows)
	}
	if aging.Principal.String() != "10000.00" || aging.Current.String() != "0.00" {
		t.Errorf("%s outstanding with %s current, want 10000.00 lent ninety days ago and none current",
			aging.Principal, aging.Current)
	}
}

func TestSettledReportsEverySettlement(t *testing.T) {
	f := newReportFixture(t)

	resp := request(t, f.handler, http.MethodGet, "/api/reports/settled"+f.period(t), f.manager, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("settled: status %d: %s", resp.Code, resp.Body)
	}

	var settled report.SettledReport
	decode(t, resp, &settled)
	if len(settled.Rows) != 1 {
		t.Fatalf("the report has %d rows, want the one settlement: %+v", len(settled.Rows), settled.Rows)
	}
	if settled.Rows[0].PaidPrincipal.String() != "1000.00" || settled.Rows[0].BalanceAfter.String() != "0.00" {
		t.Errorf("the row is %+v, want 1000.00 repaid and nothing left", settled.Rows[0])
	}
}

// A date is the only thing a caller can get wrong here, and it must come back
// as a 400 naming the parameter rather than as a 500 — the fault Step 6's
// statement handler was corrected for.
func TestAReportRefusesABadDateAndNamesIt(t *testing.T) {
	f := newReportFixture(t)

	cases := []struct {
		path, wants string
	}{
		{"/api/reports/current-advances?as_of=last+tuesday", "as_of"},
		{"/api/reports/aging?as_of=2026-13-45", "as_of"},
		{"/api/reports/roi?from=yesterday", "from"},
		{"/api/reports/settled?to=nonsense", "to"},
	}

	for _, tc := range cases {
		resp := request(t, f.handler, http.MethodGet, tc.path, f.manager, nil)
		if resp.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", tc.path, resp.Code)
			continue
		}
		if !strings.Contains(resp.Body.String(), tc.wants) {
			t.Errorf("%s: the refusal reads %q and should name %q", tc.path, resp.Body, tc.wants)
		}
	}

	// A period that runs backwards is the other thing a caller can do, and it
	// is a different sentence.
	resp := request(t, f.handler, http.MethodGet,
		"/api/reports/roi?from=2026-12-31&to=2026-01-01", f.manager, nil)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("a backwards period: status %d, want 400", resp.Code)
	}
}

func TestReportsAreManagerOnlyAndTheRefusalsAreAudited(t *testing.T) {
	f := newReportFixture(t)

	paths := []string{
		"/api/reports/current-advances",
		"/api/reports/roi",
		"/api/reports/aging",
		"/api/reports/settled",
	}
	for _, path := range paths {
		if resp := request(t, f.handler, http.MethodGet, path, f.encoder, nil); resp.Code != http.StatusForbidden {
			t.Errorf("%s as an encoder: status %d, want 403", path, resp.Code)
		}
	}

	var denials int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ?`,
		"auth.access.denied").Scan(&denials); err != nil {
		t.Fatalf("count the refusals: %v", err)
	}
	if denials != len(paths) {
		t.Errorf("%d access-denied entries, want the encoder's %d", denials, len(paths))
	}
}

func TestReportRoutesAreRegisteredWithTheRightAccess(t *testing.T) {
	handler := NewHandler(testDeps(t, openTestDB(t)))

	want := map[string]Access{
		"GET /api/reports/current-advances": ManagerOnly,
		"GET /api/reports/roi":              ManagerOnly,
		"GET /api/reports/aging":            ManagerOnly,
		"GET /api/reports/settled":          ManagerOnly,
	}

	found := map[string]bool{}
	for _, route := range handler.Routes {
		key := route.Method + " " + route.Pattern
		access, tracked := want[key]
		if !tracked {
			continue
		}
		found[key] = true

		if route.Access != access {
			t.Errorf("%s is %s, want %s", key, route.Access, access)
		}
		// A report cannot change anything, and the registry agrees: a read
		// route is not mutating and needs no audit entry.
		if route.Mutating() {
			t.Errorf("%s is registered as mutating; a report writes nothing", key)
		}
	}
	for key := range want {
		if !found[key] {
			t.Errorf("%s was never registered", key)
		}
	}
}
