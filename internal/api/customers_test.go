package api

import (
	"bytes"
	"context"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/auth"
	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
)

// customerFixture is a handler with one encoder and one manager signed up, plus
// their cookies, which is the setup every test below wants.
type customerFixture struct {
	deps    Deps
	handler *Handler
	db      *sql.DB

	encoder []*http.Cookie
	manager []*http.Cookie
}

func newCustomerFixture(t *testing.T) *customerFixture {
	t.Helper()

	database := openTestDB(t)
	deps := testDeps(t, database)
	handler := NewHandler(deps)

	seedUser(t, deps, "encoder", "counter-password", auth.RoleEncoder)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)

	return &customerFixture{
		deps:    deps,
		handler: handler,
		db:      database,
		encoder: login(t, handler, "encoder", "counter-password"),
		manager: login(t, handler, "boss", "manager-password"),
	}
}

func (f *customerFixture) create(t *testing.T, name string) customer.Customer {
	t.Helper()

	resp := request(t, f.handler, http.MethodPost, "/api/customers", f.encoder,
		customer.Draft{Name: name})
	if resp.Code != http.StatusCreated {
		t.Fatalf("create %s: status %d: %s", name, resp.Code, resp.Body)
	}

	var c customer.Customer
	decode(t, resp, &c)
	return c
}

// The everyday path: an encoder adds a farmer standing at the counter, and the
// server decides the code.
func TestAnEncoderCanAddACustomerAndTheServerAssignsTheCode(t *testing.T) {
	f := newCustomerFixture(t)

	first := f.create(t, "Juan dela Cruz")
	if first.Code != "BA-0001" {
		t.Errorf("code = %q, want BA-0001", first.Code)
	}

	second := f.create(t, "Maria Santos")
	if second.Code != "BA-0002" {
		t.Errorf("code = %q, want BA-0002", second.Code)
	}
}

// Draft has no code field, so DisallowUnknownFields refuses one outright. The
// "assigned by the server, never editable" rule therefore holds at the edge of
// the system rather than only inside it.
func TestACallerCannotSupplyOrChangeACode(t *testing.T) {
	f := newCustomerFixture(t)
	existing := f.create(t, "Juan dela Cruz")

	t.Run("not on create", func(t *testing.T) {
		resp := request(t, f.handler, http.MethodPost, "/api/customers", f.encoder,
			map[string]any{"name": "Sneaky Farmer", "code": "BA-9000"})
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", resp.Code, resp.Body)
		}
	})

	t.Run("not on update", func(t *testing.T) {
		resp := request(t, f.handler, http.MethodPut, "/api/customers/1", f.encoder,
			map[string]any{"name": "Juan dela Cruz", "code": "BA-9000"})
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", resp.Code, resp.Body)
		}

		reread := request(t, f.handler, http.MethodGet, "/api/customers/1", f.encoder, nil)
		var detail customerDetail
		decode(t, reread, &detail)
		if detail.Customer.Code != existing.Code {
			t.Errorf("code changed to %q", detail.Customer.Code)
		}
	})
}

// The balance comes from the ledger, composed at this layer so that neither
// domain package has to know about the other. With no entries posted it is
// zero, which is what Step 4 expects — and it proves the wiring works before
// Step 5 gives it something to show.
func TestCustomerDetailCarriesALedgerBalance(t *testing.T) {
	f := newCustomerFixture(t)
	f.create(t, "Juan dela Cruz")

	resp := request(t, f.handler, http.MethodGet, "/api/customers/1", f.encoder, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, resp.Body)
	}

	var detail customerDetail
	decode(t, resp, &detail)

	if detail.Customer.Name != "Juan dela Cruz" {
		t.Errorf("name = %q", detail.Customer.Name)
	}
	// Money marshals as a decimal string, never a JSON number: JavaScript's
	// float arithmetic must never be able to touch a peso.
	if detail.Balance.Principal != "0.00" || detail.Balance.Total != "0.00" {
		t.Errorf("balance = %+v, want zeroes as decimal strings", detail.Balance)
	}
}

// Deactivation is the nearest thing to a deletion the system has, so it is a
// manager's — and the refusal is itself a security event worth a row.
func TestAnEncoderCannotDeactivateACustomerAndTheRefusalIsAudited(t *testing.T) {
	f := newCustomerFixture(t)
	f.create(t, "Juan dela Cruz")

	resp := request(t, f.handler, http.MethodPost, "/api/customers/1/active", f.encoder,
		setActiveRequest{Active: false})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", resp.Code, resp.Body)
	}

	var still customerDetail
	decode(t, request(t, f.handler, http.MethodGet, "/api/customers/1", f.encoder, nil), &still)
	if !still.Customer.Active {
		t.Error("the customer was deactivated by someone who should not have been able to")
	}

	var denials int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM audit_log WHERE action = ? AND username = 'encoder'`,
		"auth.access.denied").Scan(&denials); err != nil {
		t.Fatalf("count denials: %v", err)
	}
	if denials != 1 {
		t.Errorf("%d access-denied entries, want 1", denials)
	}
}

func TestAManagerCanDeactivateAndReactivate(t *testing.T) {
	f := newCustomerFixture(t)
	f.create(t, "Juan dela Cruz")

	off := request(t, f.handler, http.MethodPost, "/api/customers/1/active", f.manager,
		setActiveRequest{Active: false})
	if off.Code != http.StatusOK {
		t.Fatalf("deactivate: status %d: %s", off.Code, off.Body)
	}

	// Gone from the default listing, still on the books.
	var page customer.Page
	decode(t, request(t, f.handler, http.MethodGet, "/api/customers", f.encoder, nil), &page)
	if page.Total != 0 {
		t.Errorf("default listing still shows %d customers", page.Total)
	}

	var all customer.Page
	decode(t, request(t, f.handler, http.MethodGet, "/api/customers?status=all", f.encoder, nil), &all)
	if all.Total != 1 {
		t.Fatalf("full listing shows %d customers, want 1 — the record must survive", all.Total)
	}

	on := request(t, f.handler, http.MethodPost, "/api/customers/1/active", f.manager,
		setActiveRequest{Active: true})
	if on.Code != http.StatusOK {
		t.Fatalf("reactivate: status %d: %s", on.Code, on.Body)
	}
}

// Every mutating customer route must leave a before and an after behind, which
// is what makes "who changed this phone number, and what was it" answerable.
func TestCustomerChangesAreAudited(t *testing.T) {
	f := newCustomerFixture(t)
	f.create(t, "Juan dela Cruz")

	resp := request(t, f.handler, http.MethodPut, "/api/customers/1", f.encoder,
		customer.Draft{Name: "Juan dela Cruz", CellNo: "0917-123-4567"})
	if resp.Code != http.StatusOK {
		t.Fatalf("update: status %d: %s", resp.Code, resp.Body)
	}

	var before, after string
	if err := f.db.QueryRow(
		`SELECT before_json, after_json FROM audit_log WHERE action = 'customer.updated'`,
	).Scan(&before, &after); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if strings.Contains(before, "09171234567") {
		t.Error("the before state already has the new number")
	}
	if !strings.Contains(after, "09171234567") {
		t.Errorf("the after state does not have the new number: %s", after)
	}
}

func TestCustomerRoutesRefuseAnonymousCallers(t *testing.T) {
	f := newCustomerFixture(t)

	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/customers"},
		{http.MethodGet, "/api/customers/1"},
		{http.MethodPost, "/api/customers"},
		{http.MethodPut, "/api/customers/1"},
		{http.MethodPost, "/api/customers/1/active"},
		{http.MethodPost, "/api/customers/import"},
	}

	for _, tc := range cases {
		resp := request(t, f.handler, tc.method, tc.path, nil, map[string]any{})
		if resp.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status %d, want 401", tc.method, tc.path, resp.Code)
		}
	}
}

// uploadCSV posts a multipart body the way the browser will.
func uploadCSV(t *testing.T, handler http.Handler, cookies []*http.Cookie, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "customers.csv")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte(body)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	for _, c := range cookies {
		req.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

const importCSV = "code,name,address,cell_no,active,notes\n" +
	"BA-0001,Juan dela Cruz,Pintor Gamu,0917-123-4567,true,\n" +
	"BA-0002,Maria Santos,Gamu,,true,\n"

func TestImportIsManagerOnly(t *testing.T) {
	f := newCustomerFixture(t)

	resp := uploadCSV(t, f.handler, f.encoder, "/api/customers/import", importCSV)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", resp.Code, resp.Body)
	}
}

// A dry run through the web door must leave the table exactly as it found it —
// the same guarantee the CLI's -check gives.
func TestImportDryRunWritesNothing(t *testing.T) {
	f := newCustomerFixture(t)

	resp := uploadCSV(t, f.handler, f.manager, "/api/customers/import?dry_run=true", importCSV)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, resp.Body)
	}

	var report customer.Report
	decode(t, resp, &report)
	if report.Accepted != 2 {
		t.Errorf("accepted %d, want 2", report.Accepted)
	}
	if report.Applied {
		t.Error("a dry run reported itself as applied")
	}
	if report.FileName != "customers.csv" {
		t.Errorf("file name = %q", report.FileName)
	}

	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM customers`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("a dry run wrote %d rows", count)
	}

	// And nothing in the audit log either: a preview did not change anything,
	// and saying it did would be a lie in the one table that exists to be
	// trusted.
	var entries int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ?`,
		customer.ActionImported).Scan(&entries); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if entries != 0 {
		t.Errorf("%d import audit entries after a dry run, want 0", entries)
	}
}

func TestImportWritesTheMasterAndOneAuditEntry(t *testing.T) {
	f := newCustomerFixture(t)

	resp := uploadCSV(t, f.handler, f.manager, "/api/customers/import", importCSV)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, resp.Body)
	}

	var report customer.Report
	decode(t, resp, &report)
	if !report.Applied || report.Accepted != 2 {
		t.Fatalf("report = %+v", report)
	}

	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM customers`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("wrote %d customers, want 2", count)
	}

	var entries int
	var username string
	if err := f.db.QueryRow(
		`SELECT COUNT(*), COALESCE(MAX(username), '') FROM audit_log WHERE action = ?`,
		customer.ActionImported).Scan(&entries, &username); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if entries != 1 {
		t.Fatalf("%d audit entries for one import, want 1", entries)
	}
	if username != "boss" {
		t.Errorf("the import was recorded against %q, want the manager who ran it", username)
	}

	// The next customer created continues from the imported high-water mark.
	if next := f.create(t, "Pedro Reyes"); next.Code != "BA-0003" {
		t.Errorf("next code = %q, want BA-0003", next.Code)
	}
}

// A file with a bad row is refused whole, and the response says which line.
func TestImportRefusesAFileWithABadRow(t *testing.T) {
	f := newCustomerFixture(t)

	resp := uploadCSV(t, f.handler, f.manager, "/api/customers/import",
		importCSV+"BA-NOPE,Bad Row,,,,\n")
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", resp.Code, resp.Body)
	}

	var report customer.Report
	decode(t, resp, &report)
	if len(report.Rejections) != 1 {
		t.Fatalf("%d rejections, want 1", len(report.Rejections))
	}
	if report.Rejections[0].Line != 4 {
		t.Errorf("rejection is on line %d, want 4", report.Rejections[0].Line)
	}

	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM customers`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("%d customers were written by a refused import", count)
	}
}

// An unreadable file is the uploader's mistake, not a fault, so they are told
// what was wrong with it rather than being handed a 500.
func TestImportExplainsAFileItCannotRead(t *testing.T) {
	f := newCustomerFixture(t)

	resp := uploadCSV(t, f.handler, f.manager, "/api/customers/import",
		"code,name,cellno\nBA-0001,Juan,0917\n")
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.Code, resp.Body)
	}

	var body errorBody
	decode(t, resp, &body)
	if !strings.Contains(body.Error, "cellno") {
		t.Errorf("the error does not name the offending column: %q", body.Error)
	}
}

// Search is the one query Step 5's picker and Step 8's reports will reuse, so
// the escaping has to hold at this layer too.
func TestSearchingForAPercentSignThroughTheAPIIsLiteral(t *testing.T) {
	f := newCustomerFixture(t)
	f.create(t, "Juan dela Cruz")
	f.create(t, "100% Palay Trading")

	var page customer.Page
	decode(t, request(t, f.handler, http.MethodGet, "/api/customers?q=%25", f.encoder, nil), &page)

	if page.Total != 1 {
		t.Fatalf("searching for a percent sign returned %d customers, want 1", page.Total)
	}
	if page.Customers[0].Name != "100% Palay Trading" {
		t.Errorf("got %q", page.Customers[0].Name)
	}
}

func TestListRejectsAnUnknownStatus(t *testing.T) {
	f := newCustomerFixture(t)

	resp := request(t, f.handler, http.MethodGet, "/api/customers?status=retired", f.encoder, nil)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.Code)
	}
}

func TestMissingCustomerIsA404(t *testing.T) {
	f := newCustomerFixture(t)

	resp := request(t, f.handler, http.MethodGet, "/api/customers/404", f.encoder, nil)
	if resp.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.Code)
	}
}

// The registry is the guarantee: every customer route states its access level
// and its audit style at registration. This asserts the table in the plan.
func TestCustomerRoutesAreRegisteredWithTheRightAccess(t *testing.T) {
	handler := NewHandler(testDeps(t, openTestDB(t)))

	want := map[string]Access{
		"GET /api/customers":              Authenticated,
		"GET /api/customers/{id}":         Authenticated,
		"POST /api/customers":             Authenticated,
		"PUT /api/customers/{id}":         Authenticated,
		"POST /api/customers/{id}/active": ManagerOnly,
		"POST /api/customers/import":      ManagerOnly,
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

// A body larger than the cap must be refused rather than read into a machine
// that is also serving the counter.
func TestImportRefusesAnOversizedUpload(t *testing.T) {
	f := newCustomerFixture(t)

	var b strings.Builder
	b.WriteString("code,name\n")
	for b.Len() < maxImportUpload+(1<<20) {
		b.WriteString("BA-0001,")
		b.WriteString(strings.Repeat("x", 500))
		b.WriteString("\n")
	}

	resp := uploadCSV(t, f.handler, f.manager, "/api/customers/import", b.String())
	if resp.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.Code)
	}

	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM customers`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("%d rows written by an oversized upload", count)
	}
}

// A sanity check that the fixture's encoder really is an encoder, so the
// manager-only tests above are not passing for the wrong reason.
func TestTheFixtureRolesAreWhatTheySay(t *testing.T) {
	f := newCustomerFixture(t)

	user, err := f.deps.Users.ByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.Role != auth.RoleEncoder {
		t.Fatalf("user 1 is %s, want encoder", user.Role)
	}
}
