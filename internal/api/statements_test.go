package api

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/auth"
	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
)

// statementFixture is a handler with staff, some farmers, and real money on the
// book — the index is only interesting against entries.
type statementFixture struct {
	deps    Deps
	handler *Handler
	encoder []*http.Cookie
	manager []*http.Cookie
	farmers []customer.Customer
}

func newStatementFixture(t *testing.T, names ...string) *statementFixture {
	t.Helper()

	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "encoder", "counter-password", auth.RoleEncoder)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)

	f := &statementFixture{
		deps:    deps,
		handler: handler,
		encoder: login(t, handler, "encoder", "counter-password"),
		manager: login(t, handler, "boss", "manager-password"),
	}
	for _, name := range names {
		resp := request(t, handler, http.MethodPost, "/api/customers", f.encoder,
			customer.Draft{Name: name})
		if resp.Code != http.StatusCreated {
			t.Fatalf("seed %s: %d %s", name, resp.Code, resp.Body)
		}
		var c customer.Customer
		decode(t, resp, &c)
		f.farmers = append(f.farmers, c)
	}
	return f
}

// lend posts a voucher of one line, which is the smallest thing that puts real
// principal on a farmer through the real routes.
func (f *statementFixture) lend(t *testing.T, c customer.Customer, pesos string) {
	t.Helper()

	body := map[string]any{
		"date":  today(t),
		"notes": "",
		"lines": []map[string]any{
			{"customer_id": c.ID, "particular": "palay advance", "amount": pesos},
		},
	}
	resp := request(t, f.handler, http.MethodPost, "/api/advances", f.encoder, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("draft a voucher: %d %s", resp.Code, resp.Body)
	}
	var voucher struct {
		ID int64 `json:"id"`
	}
	decode(t, resp, &voucher)

	resp = request(t, f.handler, http.MethodPost,
		fmt.Sprintf("/api/advances/%d/post", voucher.ID), f.encoder, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("post a voucher: %d %s", resp.Code, resp.Body)
	}
}

// lendOn is lend with the voucher dated, for the days that are not today.
func (f *statementFixture) lendOn(t *testing.T, c customer.Customer, pesos, date string) {
	t.Helper()

	body := map[string]any{
		"date":  date,
		"notes": "",
		"lines": []map[string]any{
			{"customer_id": c.ID, "particular": "palay advance", "amount": pesos},
		},
	}
	resp := request(t, f.handler, http.MethodPost, "/api/advances", f.encoder, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("draft a voucher dated %s: %d %s", date, resp.Code, resp.Body)
	}
	var voucher struct {
		ID int64 `json:"id"`
	}
	decode(t, resp, &voucher)

	resp = request(t, f.handler, http.MethodPost,
		fmt.Sprintf("/api/advances/%d/post", voucher.ID), f.encoder, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("post a voucher dated %s: %d %s", date, resp.Code, resp.Body)
	}
}

func (f *statementFixture) index(t *testing.T, cookies []*http.Cookie, query url.Values) statementIndexResponse {
	t.Helper()
	resp := request(t, f.handler, http.MethodGet, "/api/statements?"+query.Encode(), cookies, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/statements?%s = %d: %s", query.Encode(), resp.Code, resp.Body)
	}
	var page statementIndexResponse
	decode(t, resp, &page)
	return page
}

func TestStatementRoutesAreRegisteredWithTheRightAccess(t *testing.T) {
	want := map[string]Access{
		// Deliberately not ManagerOnly, unlike the four reports. The reasoning
		// is on handleListStatements; this pins the decision so that changing
		// it is a deliberate act rather than a copied line.
		"GET /api/statements":               Authenticated,
		"GET /api/customers/{id}/statement": Authenticated,
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
		t.Errorf("%s is not registered", key)
	}
}

// The invariant this whole screen rests on: a row must say the same thing the
// farmer's own statement says, on the same day.
//
// Two routes computing one figure by different paths is the disagreement Step 6
// refused to allow, and the index is the second path.
func TestEveryRowAgreesWithThatFarmersOwnStatement(t *testing.T) {
	f := newStatementFixture(t, "Juan dela Cruz", "Maria Santos", "Pedro Reyes")
	f.lend(t, f.farmers[0], "12400.00")
	f.lend(t, f.farmers[1], "32772.03")
	f.lend(t, f.farmers[1], "1250.50")
	// Pedro is lent nothing, so he has no ledger entries at all.

	page := f.index(t, f.encoder, url.Values{"status": {"all"}})
	if len(page.Rows) != 3 {
		t.Fatalf("the index has %d rows, want 3", len(page.Rows))
	}

	for _, row := range page.Rows {
		var statement statementResponse
		resp := request(t, f.handler, http.MethodGet,
			fmt.Sprintf("/api/customers/%d/statement?to=%s", row.Customer.ID, page.AsOf),
			f.encoder, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("statement for %s: %d %s", row.Customer.Code, resp.Code, resp.Body)
		}
		decode(t, resp, &statement)

		if row.Balance.Total != statement.Closing.Total {
			t.Errorf("%s: the index says %s and the statement closes at %s",
				row.Customer.Code, row.Balance.Total, statement.Closing.Total)
		}
		if row.Balance.Principal != statement.Closing.Principal {
			t.Errorf("%s: principal %s vs %s",
				row.Customer.Code, row.Balance.Principal, statement.Closing.Principal)
		}
		if row.Balance.Interest != statement.Closing.Interest {
			t.Errorf("%s: interest %s vs %s",
				row.Customer.Code, row.Balance.Interest, statement.Closing.Interest)
		}
	}
}

// The as-at date has to actually mean something. Nothing else in these tests
// asks for a day other than today, so without this the index could ignore the
// parameter entirely and every one of them would still pass.
func TestTheIndexAnswersForTheDayItIsAsked(t *testing.T) {
	f := newStatementFixture(t, "Juan dela Cruz")
	juan := f.farmers[0]

	station := time.FixedZone("PHT", 8*60*60)
	yesterday := ledger.DateOf(time.Now().In(station).AddDate(0, 0, -1))
	dayBefore := ledger.DateOf(time.Now().In(station).AddDate(0, 0, -2))

	f.lendOn(t, juan, "4000.00", yesterday.String())

	// On the day of the advance, it is there.
	onTheDay := f.index(t, f.encoder, url.Values{"as_of": {yesterday.String()}})
	if got := onTheDay.Rows[0].Balance.Total; got != "4000.00" {
		t.Errorf("as at %s the balance reads %q, want 4000.00", yesterday, got)
	}

	// The day before it, it is not — and this is what fails if the handler
	// computes today's balance whatever it was asked for.
	before := f.index(t, f.encoder, url.Values{"as_of": {dayBefore.String()}})
	if got := before.Rows[0].Balance.Total; got != "0.00" {
		t.Errorf("as at %s — before the advance — the balance reads %q, want 0.00; "+
			"the as_of parameter is being ignored", dayBefore, got)
	}
	if before.AsOf != dayBefore.String() {
		t.Errorf("the response says as_of %q, want %q", before.AsOf, dayBefore)
	}

	// And it agrees with the statement cut at the same date, which is the
	// invariant the whole screen rests on, checked away from today.
	var statement statementResponse
	resp := request(t, f.handler, http.MethodGet,
		fmt.Sprintf("/api/customers/%d/statement?to=%s", juan.ID, dayBefore), f.encoder, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("statement: %d %s", resp.Code, resp.Body)
	}
	decode(t, resp, &statement)
	if before.Rows[0].Balance.Total != statement.Closing.Total {
		t.Errorf("as at %s the index says %s and the statement closes at %s",
			dayBefore, before.Rows[0].Balance.Total, statement.Closing.Total)
	}
}

// BalancesAt leaves out customers who owe nothing — deliberately, because it
// answers "who owes something" and a run padded with zeroes hides the rows that
// matter. The index wants the opposite: it is a way of *finding* a farmer.
func TestAFarmerWhoHasNeverBorrowedIsListedAtZero(t *testing.T) {
	f := newStatementFixture(t, "Juan dela Cruz", "Pedro Reyes")
	f.lend(t, f.farmers[0], "500.00")

	page := f.index(t, f.encoder, url.Values{})

	var pedro *statementIndexRow
	for i := range page.Rows {
		if page.Rows[i].Customer.Name == "Pedro Reyes" {
			pedro = &page.Rows[i]
		}
	}
	if pedro == nil {
		t.Fatalf("a farmer with no entries was dropped from the index; rows were %d", len(page.Rows))
	}
	for field, got := range map[string]string{
		"principal": pedro.Balance.Principal,
		"interest":  pedro.Balance.Interest,
		"total":     pedro.Balance.Total,
	} {
		if got != "0.00" {
			t.Errorf("Pedro's %s reads %q, want \"0.00\"", field, got)
		}
	}
	if pedro.Balance.LastActivity != "" {
		t.Errorf("Pedro has a last activity of %q and has never borrowed", pedro.Balance.LastActivity)
	}
}

// Search must behave exactly as /customers does, because it is the same query —
// including Step 4's rule that a typed % is a percent sign and not a wildcard.
func TestTheIndexSearchesTheSameWayTheCustomerListDoes(t *testing.T) {
	f := newStatementFixture(t, "Juan dela Cruz", "Maria Santos", "100% Cotton Supply")

	for _, text := range []string{"Maria", "BA-0001", "100%", "%"} {
		index := f.index(t, f.encoder, url.Values{"q": {text}, "status": {"all"}})

		resp := request(t, f.handler, http.MethodGet,
			"/api/customers?"+url.Values{"q": {text}, "status": {"all"}}.Encode(), f.encoder, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("customers?q=%s: %d", text, resp.Code)
		}
		var list customer.Page
		decode(t, resp, &list)

		if index.Total != list.Total {
			t.Errorf("q=%q: the index found %d and /customers found %d", text, index.Total, list.Total)
		}
		for i := range index.Rows {
			if i >= len(list.Customers) {
				break
			}
			if index.Rows[i].Customer.ID != list.Customers[i].ID {
				t.Errorf("q=%q: row %d is %s in the index and %s in /customers",
					text, i, index.Rows[i].Customer.Code, list.Customers[i].Code)
			}
		}
	}
}

func TestTheIndexPagesLikeTheCustomerList(t *testing.T) {
	names := make([]string, 0, 30)
	for i := range 30 {
		names = append(names, fmt.Sprintf("Farmer %02d", i))
	}
	f := newStatementFixture(t, names...)
	f.lend(t, f.farmers[0], "100.00")
	f.lend(t, f.farmers[29], "250.00")

	seen := map[int64]int{}
	for page := 1; page <= 3; page++ {
		got := f.index(t, f.encoder, url.Values{
			"page": {fmt.Sprint(page)}, "per_page": {"10"}, "status": {"all"},
		})
		if got.Total != 30 {
			t.Errorf("page %d reports %d total, want 30", page, got.Total)
		}
		if len(got.Rows) != 10 {
			t.Errorf("page %d has %d rows, want 10", page, len(got.Rows))
		}
		for _, row := range got.Rows {
			seen[row.Customer.ID]++
		}
	}
	if len(seen) != 30 {
		t.Errorf("three pages of ten showed %d distinct farmers, want 30", len(seen))
	}
	for id, times := range seen {
		if times > 1 {
			t.Errorf("customer %d appeared on %d pages", id, times)
		}
	}
}

func TestAnEncoderCanReachTheIndexAndABadDateIsRefused(t *testing.T) {
	f := newStatementFixture(t, "Juan dela Cruz")

	// The departure from the reports, checked from the encoder's side.
	if resp := request(t, f.handler, http.MethodGet, "/api/statements", f.encoder, nil); resp.Code != http.StatusOK {
		t.Errorf("an encoder on the statements index = %d, want 200: %s", resp.Code, resp.Body)
	}
	// And the reports are still shut, so this test is not passing because the
	// whole gate came off.
	if resp := request(t, f.handler, http.MethodGet, "/api/reports/aging", f.encoder, nil); resp.Code != http.StatusForbidden {
		t.Errorf("an encoder on the aging report = %d, want 403 — the manager gate has gone", resp.Code)
	}

	resp := request(t, f.handler, http.MethodGet, "/api/statements?as_of=last-tuesday", f.encoder, nil)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("as_of=last-tuesday = %d, want 400", resp.Code)
	}
	var body errorBody
	decode(t, resp, &body)
	if !contains(body.Error, "as_of") {
		t.Errorf("the refusal does not name the parameter: %q", body.Error)
	}
}
