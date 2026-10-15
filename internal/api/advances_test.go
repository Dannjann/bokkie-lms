package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/auth"
	"github.com/Dannjann/bokkie-lms/internal/domain/advance"
	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
)

type advanceFixture struct {
	deps    Deps
	handler *Handler
	db      *sql.DB

	encoder []*http.Cookie
	manager []*http.Cookie

	farmers []customer.Customer
}

func newAdvanceFixture(t *testing.T) *advanceFixture {
	t.Helper()

	database := openTestDB(t)
	deps := testDeps(t, database)
	handler := NewHandler(deps)

	seedUser(t, deps, "encoder", "counter-password", auth.RoleEncoder)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)

	f := &advanceFixture{
		deps:    deps,
		handler: handler,
		db:      database,
		encoder: login(t, handler, "encoder", "counter-password"),
		manager: login(t, handler, "boss", "manager-password"),
	}

	for _, name := range []string{"Juan dela Cruz", "Maria Santos"} {
		resp := request(t, handler, http.MethodPost, "/api/customers", f.encoder,
			customer.Draft{Name: name})
		if resp.Code != http.StatusCreated {
			t.Fatalf("seed %s: status %d: %s", name, resp.Code, resp.Body)
		}
		var c customer.Customer
		decode(t, resp, &c)
		f.farmers = append(f.farmers, c)
	}
	return f
}

// draft is the body an encoder's browser sends: a date, and a line per farmer.
func (f *advanceFixture) draft(t *testing.T, pesos ...string) map[string]any {
	t.Helper()

	lines := make([]map[string]any, 0, len(pesos))
	for i, amount := range pesos {
		lines = append(lines, map[string]any{
			"customer_id": f.farmers[i%len(f.farmers)].ID,
			"particular":  "palay advance",
			"amount":      amount,
		})
	}
	return map[string]any{"date": today(t), "notes": "", "lines": lines}
}

// today is the station's date as the browser would send it. The server refuses
// anything later, so a test cannot hard-code a date and still be passing next
// year.
func today(t *testing.T) string {
	t.Helper()
	return ledger.DateOf(time.Now().In(time.FixedZone("PHT", 8*60*60))).String()
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func (f *advanceFixture) create(t *testing.T, body any) advance.Voucher {
	t.Helper()

	resp := request(t, f.handler, http.MethodPost, "/api/advances", f.encoder, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create a draft: status %d: %s", resp.Code, resp.Body)
	}
	var v advance.Voucher
	decode(t, resp, &v)
	return v
}

func (f *advanceFixture) balance(t *testing.T, c customer.Customer) string {
	t.Helper()

	var detail customerDetail
	decode(t, request(t, f.handler, http.MethodGet, "/api/customers/"+itoa(c.ID), f.encoder, nil), &detail)
	return detail.Balance.Principal
}

// The everyday path, end to end: an encoder types a voucher, saves it, posts
// it, and two farmers owe money that was not there before.
func TestAnEncoderEntersAndPostsAVoucher(t *testing.T) {
	f := newAdvanceFixture(t)

	draft := f.create(t, f.draft(t, "500.00", "700.00"))
	if draft.Status != advance.StatusDraft || draft.RefNo != 0 {
		t.Fatalf("a new voucher is %s with Ref. No. %d, want a draft with none", draft.Status, draft.RefNo)
	}
	if got := f.balance(t, f.farmers[0]); got != "0.00" {
		t.Errorf("a draft moved money: customer 1 is at %s", got)
	}

	resp := request(t, f.handler, http.MethodPost, "/api/advances/"+itoa(draft.ID)+"/post", f.encoder, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("post: status %d: %s", resp.Code, resp.Body)
	}
	var posted advance.Voucher
	decode(t, resp, &posted)

	// 1426 continues the legacy sequence: refno.start defaults to the old system's next number.
	if posted.Status != advance.StatusPosted || posted.RefNo != 1426 {
		t.Errorf("the posted voucher is %s with Ref. No. %d, want posted and numbered", posted.Status, posted.RefNo)
	}
	if got := f.balance(t, f.farmers[0]); got != "500.00" {
		t.Errorf("customer 1 is at %s after posting, want 500.00", got)
	}
	if got := f.balance(t, f.farmers[1]); got != "700.00" {
		t.Errorf("customer 2 is at %s after posting, want 700.00", got)
	}

	// The detail screen shows what the voucher actually did to the ledger,
	// composed from the document and the entries behind it.
	var detail advanceDetail
	decode(t, request(t, f.handler, http.MethodGet, "/api/advances/"+itoa(draft.ID), f.encoder, nil), &detail)
	if len(detail.Entries) != 2 {
		t.Fatalf("the posted voucher shows %d entries, want one per line", len(detail.Entries))
	}
	if detail.Entries[0].Principal != "500.00" || detail.Entries[0].Type != "advance" {
		t.Errorf("the first entry is a %s of %s", detail.Entries[0].Type, detail.Entries[0].Principal)
	}
}

func TestPostingTwiceIsAConflictNotASecondVoucher(t *testing.T) {
	f := newAdvanceFixture(t)
	draft := f.create(t, f.draft(t, "500.00"))

	first := request(t, f.handler, http.MethodPost, "/api/advances/"+itoa(draft.ID)+"/post", f.encoder, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("post: status %d: %s", first.Code, first.Body)
	}
	second := request(t, f.handler, http.MethodPost, "/api/advances/"+itoa(draft.ID)+"/post", f.encoder, nil)
	if second.Code != http.StatusConflict {
		t.Errorf("the second post returned %d, want 409", second.Code)
	}
	if got := f.balance(t, f.farmers[0]); got != "500.00" {
		t.Errorf("customer 1 is at %s after a double-clicked Post, want 500.00", got)
	}
}

func TestOnlyAManagerCanVoid(t *testing.T) {
	f := newAdvanceFixture(t)

	draft := f.create(t, f.draft(t, "500.00"))
	if resp := request(t, f.handler, http.MethodPost, "/api/advances/"+itoa(draft.ID)+"/post",
		f.encoder, nil); resp.Code != http.StatusOK {
		t.Fatalf("post: status %d: %s", resp.Code, resp.Body)
	}

	refused := request(t, f.handler, http.MethodPost, "/api/advances/"+itoa(draft.ID)+"/void",
		f.encoder, voidRequest{Reason: "wrong farmer"})
	if refused.Code != http.StatusForbidden {
		t.Fatalf("an encoder's void returned %d, want 403", refused.Code)
	}
	if got := f.balance(t, f.farmers[0]); got != "500.00" {
		t.Errorf("a refused void moved money: customer 1 is at %s", got)
	}

	var denials int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM audit_log WHERE action = ? AND username = 'encoder'`,
		"auth.access.denied").Scan(&denials); err != nil {
		t.Fatal(err)
	}
	if denials != 1 {
		t.Errorf("%d access-denied entries, want the encoder's one", denials)
	}

	// A reason is not optional, even for a manager.
	if resp := request(t, f.handler, http.MethodPost, "/api/advances/"+itoa(draft.ID)+"/void",
		f.manager, voidRequest{}); resp.Code != http.StatusBadRequest {
		t.Errorf("voiding with no reason returned %d, want 400", resp.Code)
	}

	ok := request(t, f.handler, http.MethodPost, "/api/advances/"+itoa(draft.ID)+"/void",
		f.manager, voidRequest{Reason: "wrong farmer"})
	if ok.Code != http.StatusOK {
		t.Fatalf("the manager's void returned %d: %s", ok.Code, ok.Body)
	}
	if got := f.balance(t, f.farmers[0]); got != "0.00" {
		t.Errorf("customer 1 is at %s after the void, want 0.00", got)
	}

	// Both halves stay on the record.
	var detail advanceDetail
	decode(t, request(t, f.handler, http.MethodGet, "/api/advances/"+itoa(draft.ID), f.manager, nil), &detail)
	if detail.Voucher.Status != advance.StatusVoided || len(detail.Entries) != 2 {
		t.Errorf("the voided voucher is %s with %d entries, want voided with an advance and its reversal",
			detail.Voucher.Status, len(detail.Entries))
	}
}

func TestDeletingADraftKeepsWhatItSaid(t *testing.T) {
	f := newAdvanceFixture(t)
	draft := f.create(t, f.draft(t, "500.00"))

	resp := request(t, f.handler, http.MethodDelete, "/api/advances/"+itoa(draft.ID), f.encoder, nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("delete a draft: status %d: %s", resp.Code, resp.Body)
	}
	if gone := request(t, f.handler, http.MethodGet, "/api/advances/"+itoa(draft.ID), f.encoder, nil); gone.Code != http.StatusNotFound {
		t.Errorf("reading a deleted draft returned %d, want 404", gone.Code)
	}

	var before string
	if err := f.db.QueryRow(
		`SELECT COALESCE(before_json, '') FROM audit_log WHERE action = 'advance.draft.deleted'`).
		Scan(&before); err != nil {
		t.Fatalf("read the audit entry: %v", err)
	}
	if before == "" {
		t.Error("the deleted draft left no record of what it said")
	}
}

// A posted voucher cannot be edited or deleted through the API either, which is
// the same rule the package and the database each enforce on their own.
func TestAPostedVoucherIsClosedToEditing(t *testing.T) {
	f := newAdvanceFixture(t)
	draft := f.create(t, f.draft(t, "500.00"))
	request(t, f.handler, http.MethodPost, "/api/advances/"+itoa(draft.ID)+"/post", f.encoder, nil)

	edited := request(t, f.handler, http.MethodPut, "/api/advances/"+itoa(draft.ID), f.encoder,
		f.draft(t, "100.00"))
	if edited.Code != http.StatusConflict {
		t.Errorf("editing a posted voucher returned %d, want 409", edited.Code)
	}
	deleted := request(t, f.handler, http.MethodDelete, "/api/advances/"+itoa(draft.ID), f.encoder, nil)
	if deleted.Code != http.StatusConflict {
		t.Errorf("deleting a posted voucher returned %d, want 409", deleted.Code)
	}
}

// Draft has no ref_no and no status field, so a caller cannot supply either:
// decodeJSON refuses unknown fields before any handler runs.
func TestACallerCannotSupplyARefNoOrAStatus(t *testing.T) {
	f := newAdvanceFixture(t)

	for _, field := range []string{"ref_no", "status"} {
		body := f.draft(t, "500.00")
		body[field] = "1425"

		if resp := request(t, f.handler, http.MethodPost, "/api/advances", f.encoder, body); resp.Code != http.StatusBadRequest {
			t.Errorf("a body carrying %q returned %d, want 400", field, resp.Code)
		}
	}
}

func TestAdvanceRoutesAreRegisteredWithTheRightAccess(t *testing.T) {
	handler := NewHandler(testDeps(t, openTestDB(t)))

	want := map[string]Access{
		"GET /api/advances":            Authenticated,
		"GET /api/advances/{id}":       Authenticated,
		"POST /api/advances":           Authenticated,
		"PUT /api/advances/{id}":       Authenticated,
		"DELETE /api/advances/{id}":    Authenticated,
		"POST /api/advances/{id}/post": Authenticated,
		"POST /api/advances/{id}/void": ManagerOnly,
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
