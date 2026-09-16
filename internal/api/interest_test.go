package api

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/auth"
	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
	"github.com/Dannjann/bokkie-lms/internal/domain/interest"
)

type interestFixture struct {
	deps    Deps
	handler *Handler
	db      *sql.DB

	encoder []*http.Cookie
	manager []*http.Cookie

	farmer customer.Customer
}

// newInterestFixture is a station with one farmer who has been advanced
// ₱5,000.00 through the real voucher route, and a rule set through the real
// settings route — because what this step is about is what happens to money
// that is genuinely on the books.
func newInterestFixture(t *testing.T) *interestFixture {
	t.Helper()

	database := openTestDB(t)
	deps := testDeps(t, database)
	handler := NewHandler(deps)

	seedUser(t, deps, "encoder", "counter-password", auth.RoleEncoder)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)

	f := &interestFixture{
		deps:    deps,
		handler: handler,
		db:      database,
		encoder: login(t, handler, "encoder", "counter-password"),
		manager: login(t, handler, "boss", "manager-password"),
	}

	resp := request(t, handler, http.MethodPost, "/api/customers", f.encoder,
		customer.Draft{Name: "Juan dela Cruz"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("seed a farmer: status %d: %s", resp.Code, resp.Body)
	}
	decode(t, resp, &f.farmer)

	voucher := map[string]any{
		"date": today(t), "notes": "",
		"lines": []map[string]any{{
			"customer_id": f.farmer.ID,
			"particular":  "palay advance",
			"amount":      "5000.00",
		}},
	}
	created := request(t, handler, http.MethodPost, "/api/advances", f.encoder, voucher)
	if created.Code != http.StatusCreated {
		t.Fatalf("seed a voucher: status %d: %s", created.Code, created.Body)
	}
	var v struct {
		ID int64 `json:"id"`
	}
	decode(t, created, &v)
	if posted := request(t, handler, http.MethodPost, "/api/advances/"+itoa(v.ID)+"/post",
		f.encoder, nil); posted.Code != http.StatusOK {
		t.Fatalf("post the seed voucher: status %d: %s", posted.Code, posted.Body)
	}
	return f
}

// setRule puts a rule in through the settings route a manager would use.
func (f *interestFixture) setRule(t *testing.T, method string, rateBps string) {
	t.Helper()

	for _, s := range []struct{ key, value string }{
		{"interest.method", method},
		{"interest.rate_bps", rateBps},
	} {
		resp := request(t, f.handler, http.MethodPut, "/api/settings", f.manager,
			map[string]string{"key": s.key, "value": s.value})
		if resp.Code != http.StatusOK {
			t.Fatalf("set %s: status %d: %s", s.key, resp.Code, resp.Body)
		}
	}
}

// period is a run over today alone, which is when the seeded advance is dated.
func (f *interestFixture) period() map[string]any {
	return map[string]any{"period_from": "", "period_to": "", "notes": "September interest"}
}

func (f *interestFixture) create(t *testing.T, body map[string]any) interest.Run {
	t.Helper()

	resp := request(t, f.handler, http.MethodPost, "/api/interest-runs", f.manager, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("compute a run: status %d: %s", resp.Code, resp.Body)
	}
	var run interest.Run
	decode(t, resp, &run)
	return run
}

func (f *interestFixture) balance(t *testing.T) (principal, interestOwed string) {
	t.Helper()

	var detail customerDetail
	decode(t, request(t, f.handler, http.MethodGet, "/api/customers/"+itoa(f.farmer.ID), f.manager, nil), &detail)
	return detail.Balance.Principal, detail.Balance.Interest
}

// The everyday path, end to end: a manager charges a month's interest, sees it
// on the farmer's statement, and takes it back off again.
func TestAManagerRunsInterestAndUndoesIt(t *testing.T) {
	f := newInterestFixture(t)
	f.setRule(t, "monthly-on-balance", "300")

	day := today(t)
	run := f.create(t, map[string]any{"period_from": day, "period_to": day, "notes": "March interest"})

	if run.Status != interest.StatusDraft || run.RunNo != 0 {
		t.Fatalf("a new run is %s with number %d, want a draft with none", run.Status, run.RunNo)
	}
	if len(run.Lines) != 1 || run.Lines[0].Charge.String() != "150.00" {
		t.Fatalf("the preview charges %+v, want one line of 150.00", run.Lines)
	}
	if _, owed := f.balance(t); owed != "0.00" {
		t.Errorf("a preview charged %s of interest; a draft charges nothing", owed)
	}

	resp := request(t, f.handler, http.MethodPost, "/api/interest-runs/"+itoa(run.ID)+"/post", f.manager, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("post the run: status %d: %s", resp.Code, resp.Body)
	}
	var posted interest.Run
	decode(t, resp, &posted)
	if posted.RunNo != 1 || posted.Status != interest.StatusPosted {
		t.Fatalf("the posted run is %s numbered %d, want posted as 1", posted.Status, posted.RunNo)
	}

	principal, owed := f.balance(t)
	if principal != "5000.00" || owed != "150.00" {
		t.Errorf("the farmer owes %s / %s, want 5000.00 principal and 150.00 interest", principal, owed)
	}

	// The statement calls it what staff would call it.
	var stmt statementResponse
	decode(t, request(t, f.handler, http.MethodGet,
		"/api/customers/"+itoa(f.farmer.ID)+"/statement", f.manager, nil), &stmt)

	var labelled bool
	for _, row := range stmt.Rows {
		if row.Entry.Type == "interest" {
			labelled = true
			if row.Document != "Interest run 1" {
				t.Errorf("the interest row is labelled %q, want \"Interest run 1\"", row.Document)
			}
		}
	}
	if !labelled {
		t.Error("the statement has no interest row on it at all")
	}

	// And it comes off again, exactly.
	voided := request(t, f.handler, http.MethodPost, "/api/interest-runs/"+itoa(run.ID)+"/void",
		f.manager, map[string]string{"reason": "charged the wrong month"})
	if voided.Code != http.StatusOK {
		t.Fatalf("void the run: status %d: %s", voided.Code, voided.Body)
	}
	if principal, owed := f.balance(t); principal != "5000.00" || owed != "0.00" {
		t.Errorf("after the void the farmer owes %s / %s, want 5000.00 and nothing", principal, owed)
	}
}

// Nothing charges interest until somebody sets a rule, and the refusal names
// the setting to go and change rather than saying "bad request".
func TestARunCannotBeComputedWithNoRuleSet(t *testing.T) {
	f := newInterestFixture(t)

	day := today(t)
	resp := request(t, f.handler, http.MethodPost, "/api/interest-runs", f.manager,
		map[string]any{"period_from": day, "period_to": day})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("computing a run with no rule: status %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body.String(), "Settings") {
		t.Errorf("the refusal reads %q; it should say where to go and set one", resp.Body)
	}
}

// An encoder cannot reach any of it, the preview included.
func TestAnEncoderCannotTouchInterestAtAll(t *testing.T) {
	f := newInterestFixture(t)
	f.setRule(t, "monthly-on-balance", "300")

	day := today(t)
	run := f.create(t, map[string]any{"period_from": day, "period_to": day})

	attempts := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/interest-runs", nil},
		{http.MethodGet, "/api/interest-runs/" + itoa(run.ID), nil},
		{http.MethodPost, "/api/interest-runs", map[string]any{"period_from": day, "period_to": day}},
		{http.MethodPut, "/api/interest-runs/" + itoa(run.ID), map[string]any{"period_from": day, "period_to": day}},
		{http.MethodDelete, "/api/interest-runs/" + itoa(run.ID), nil},
		{http.MethodPost, "/api/interest-runs/" + itoa(run.ID) + "/post", nil},
		{http.MethodPost, "/api/interest-runs/" + itoa(run.ID) + "/void", map[string]string{"reason": "no"}},
	}

	for _, tc := range attempts {
		resp := request(t, f.handler, tc.method, tc.path, f.encoder, tc.body)
		if resp.Code != http.StatusForbidden {
			t.Errorf("%s %s as an encoder: status %d, want 403", tc.method, tc.path, resp.Code)
		}
	}

	// And every one of those refusals is on the record.
	var denials int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ?`,
		"auth.access.denied").Scan(&denials); err != nil {
		t.Fatalf("count the refusals: %v", err)
	}
	if denials != len(attempts) {
		t.Errorf("%d access-denied entries, want the encoder's %d", denials, len(attempts))
	}
}

// A caller cannot decide what a run charges. interest.Draft carries a period
// and a note, and decodeJSON refuses everything else — which is what makes
// "charge this farmer ₱10,000" unexpressible rather than merely refused.
func TestACallerCannotSupplyLinesANumberOrAStatus(t *testing.T) {
	f := newInterestFixture(t)
	f.setRule(t, "monthly-on-balance", "300")
	day := today(t)

	bodies := []map[string]any{
		{"period_from": day, "period_to": day, "run_no": 7},
		{"period_from": day, "period_to": day, "status": "posted"},
		{"period_from": day, "period_to": day, "rule": map[string]any{"rate_bps": 9999}},
		{"period_from": day, "period_to": day, "lines": []map[string]any{
			{"customer_id": 1, "base": "1.00", "charge": "10000.00"}}},
	}

	for _, body := range bodies {
		resp := request(t, f.handler, http.MethodPost, "/api/interest-runs", f.manager, body)
		if resp.Code != http.StatusBadRequest {
			t.Errorf("a caller supplying %v: status %d, want 400", body, resp.Code)
		}
	}
}

// The two refusals that are not the caller's fault: both are 409, and both
// carry the sentence the domain wrote, because the sentence is the whole value
// of the refusal.
func TestChargingAPeriodTwiceIsRefusedWithAnExplanation(t *testing.T) {
	f := newInterestFixture(t)
	f.setRule(t, "monthly-on-balance", "300")
	day := today(t)

	first := f.create(t, map[string]any{"period_from": day, "period_to": day})
	if resp := request(t, f.handler, http.MethodPost, "/api/interest-runs/"+itoa(first.ID)+"/post",
		f.manager, nil); resp.Code != http.StatusOK {
		t.Fatalf("post the first run: status %d: %s", resp.Code, resp.Body)
	}

	again := f.create(t, map[string]any{"period_from": day, "period_to": day})
	resp := request(t, f.handler, http.MethodPost, "/api/interest-runs/"+itoa(again.ID)+"/post", f.manager, nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("charging the same period twice: status %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body.String(), "already covers") {
		t.Errorf("the refusal reads %q; it should name the run that already covers the period", resp.Body)
	}

	if _, owed := f.balance(t); owed != "150.00" {
		t.Errorf("the farmer owes %s of interest, want the one charge of 150.00", owed)
	}
}

func TestPostingAStalePreviewIsRefusedWithTheFarmerNamed(t *testing.T) {
	f := newInterestFixture(t)
	f.setRule(t, "monthly-on-balance", "300")
	day := today(t)

	run := f.create(t, map[string]any{"period_from": day, "period_to": day})

	// A payment arrives at the counter while the run sits on somebody's screen.
	paid := request(t, f.handler, http.MethodPost, "/api/payments", f.encoder, map[string]any{
		"customer_id": f.farmer.ID, "date": day, "amount": "2000.00",
		"method": "cash", "particular": "partial",
	})
	if paid.Code != http.StatusCreated {
		t.Fatalf("take a payment: status %d: %s", paid.Code, paid.Body)
	}
	var p struct {
		ID int64 `json:"id"`
	}
	decode(t, paid, &p)
	if resp := request(t, f.handler, http.MethodPost, "/api/payments/"+itoa(p.ID)+"/post",
		f.encoder, nil); resp.Code != http.StatusOK {
		t.Fatalf("post the payment: status %d: %s", resp.Code, resp.Body)
	}

	resp := request(t, f.handler, http.MethodPost, "/api/interest-runs/"+itoa(run.ID)+"/post", f.manager, nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("posting a stale preview: status %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body.String(), f.farmer.Code) {
		t.Errorf("the refusal reads %q; it should name the farmer whose figure moved", resp.Body)
	}

	// Recomputing is the fix, through the route a Refresh button would call.
	fresh := request(t, f.handler, http.MethodPut, "/api/interest-runs/"+itoa(run.ID), f.manager,
		map[string]any{"period_from": day, "period_to": day})
	if fresh.Code != http.StatusOK {
		t.Fatalf("recompute: status %d: %s", fresh.Code, fresh.Body)
	}
	var recomputed interest.Run
	decode(t, fresh, &recomputed)
	if recomputed.Lines[0].Charge.String() != "90.00" {
		t.Fatalf("the recomputed charge is %s, want 90.00", recomputed.Lines[0].Charge)
	}
	if resp := request(t, f.handler, http.MethodPost, "/api/interest-runs/"+itoa(run.ID)+"/post",
		f.manager, nil); resp.Code != http.StatusOK {
		t.Fatalf("posting the recomputed run: status %d: %s", resp.Code, resp.Body)
	}
}

func TestInterestRoutesAreRegisteredWithTheRightAccess(t *testing.T) {
	handler := NewHandler(testDeps(t, openTestDB(t)))

	// Every one of them is a manager's, reads included. See registerRoutes for
	// why this section is the one that does not open its everyday half.
	want := map[string]Access{
		"GET /api/interest-runs":            ManagerOnly,
		"GET /api/interest-runs/{id}":       ManagerOnly,
		"POST /api/interest-runs":           ManagerOnly,
		"PUT /api/interest-runs/{id}":       ManagerOnly,
		"DELETE /api/interest-runs/{id}":    ManagerOnly,
		"POST /api/interest-runs/{id}/post": ManagerOnly,
		"POST /api/interest-runs/{id}/void": ManagerOnly,
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
		if route.Mutating() && !route.Audited() {
			t.Errorf("%s changes state but writes no audit entry", key)
		}
	}
	for key := range want {
		if !found[key] {
			t.Errorf("%s was never registered", key)
		}
	}
}

// Task 7.9: the tests that were impossible before this step.
//
// Step 6 shipped payment.applies with nothing to exercise it — every payment
// posted before now was entirely principal whatever the setting said, because
// nothing in the system charged interest. These divide a real payment against
// interest a real run put on the books.
func TestAPaymentAfterARunDividesByTheStationsRule(t *testing.T) {
	cases := []struct {
		applies             string
		principal, interest string
	}{
		// ₱1,000.00 against a farmer owing ₱5,000.00 of principal and ₱150.00
		// of interest. The deltas are negative: a payment reduces what is owed.
		{"interest-first", "-850.00", "-150.00"},
		{"principal-first", "-1000.00", "0.00"},
	}

	for _, tc := range cases {
		t.Run(tc.applies, func(t *testing.T) {
			f := newInterestFixture(t)
			f.setRule(t, "monthly-on-balance", "300")
			day := today(t)

			if resp := request(t, f.handler, http.MethodPut, "/api/settings", f.manager,
				map[string]string{"key": "payment.applies", "value": tc.applies}); resp.Code != http.StatusOK {
				t.Fatalf("set payment.applies: status %d: %s", resp.Code, resp.Body)
			}

			run := f.create(t, map[string]any{"period_from": day, "period_to": day})
			if resp := request(t, f.handler, http.MethodPost, "/api/interest-runs/"+itoa(run.ID)+"/post",
				f.manager, nil); resp.Code != http.StatusOK {
				t.Fatalf("post the run: status %d: %s", resp.Code, resp.Body)
			}
			if _, owed := f.balance(t); owed != "150.00" {
				t.Fatalf("the farmer owes %s of interest before paying, want 150.00", owed)
			}

			paid := request(t, f.handler, http.MethodPost, "/api/payments", f.encoder, map[string]any{
				"customer_id": f.farmer.ID, "date": day, "amount": "1000.00",
				"method": "cash", "particular": "",
			})
			if paid.Code != http.StatusCreated {
				t.Fatalf("take a payment: status %d: %s", paid.Code, paid.Body)
			}
			var p struct {
				ID int64 `json:"id"`
			}
			decode(t, paid, &p)
			if resp := request(t, f.handler, http.MethodPost, "/api/payments/"+itoa(p.ID)+"/post",
				f.encoder, nil); resp.Code != http.StatusOK {
				t.Fatalf("post the payment: status %d: %s", resp.Code, resp.Body)
			}

			// The split is read back off the ledger entry, which is the only
			// place it is stored.
			var detail paymentDetail
			decode(t, request(t, f.handler, http.MethodGet, "/api/payments/"+itoa(p.ID), f.encoder, nil), &detail)
			if len(detail.Entries) != 1 {
				t.Fatalf("the payment wrote %d entries, want one", len(detail.Entries))
			}
			got := detail.Entries[0]
			if got.Principal != tc.principal || got.Interest != tc.interest {
				t.Errorf("%s divided the payment into %s principal and %s interest, want %s and %s",
					tc.applies, got.Principal, got.Interest, tc.principal, tc.interest)
			}
		})
	}
}
