package api

import (
	"database/sql"
	"net/http"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/auth"
	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
	"github.com/Dannjann/bokkie-lms/internal/domain/payment"
)

type paymentFixture struct {
	deps    Deps
	handler *Handler
	db      *sql.DB

	encoder []*http.Cookie
	manager []*http.Cookie

	farmer customer.Customer
}

// newPaymentFixture is a station with one farmer who has been advanced
// ₱5,000.00 — because a payment screen with nothing to pay off is not the
// screen anybody uses.
func newPaymentFixture(t *testing.T) *paymentFixture {
	t.Helper()

	database := openTestDB(t)
	deps := testDeps(t, database)
	handler := NewHandler(deps)

	seedUser(t, deps, "encoder", "counter-password", auth.RoleEncoder)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)

	f := &paymentFixture{
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

	// Lend them something, through the real voucher route, so the balance a
	// payment is split against was put there the way it would be in life.
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

// draft is the body an encoder's browser sends.
func (f *paymentFixture) draft(amount string) map[string]any {
	return map[string]any{
		"customer_id": f.farmer.ID,
		"date":        "",
		"amount":      amount,
		"method":      "cash",
		"particular":  "partial payment",
	}
}

func (f *paymentFixture) create(t *testing.T, body any) payment.Payment {
	t.Helper()

	resp := request(t, f.handler, http.MethodPost, "/api/payments", f.encoder, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create a draft: status %d: %s", resp.Code, resp.Body)
	}
	var p payment.Payment
	decode(t, resp, &p)
	return p
}

func (f *paymentFixture) balance(t *testing.T) string {
	t.Helper()

	var detail customerDetail
	decode(t, request(t, f.handler, http.MethodGet, "/api/customers/"+itoa(f.farmer.ID), f.encoder, nil), &detail)
	return detail.Balance.Principal
}

// The everyday path, end to end: a farmer pays at the counter, and owes less.
func TestAnEncoderTakesAndPostsAPayment(t *testing.T) {
	f := newPaymentFixture(t)

	draft := f.create(t, f.draft("2000.00"))
	if draft.Status != payment.StatusDraft || draft.OrNo != 0 {
		t.Fatalf("a new payment is %s with OR No. %d, want a draft with none", draft.Status, draft.OrNo)
	}
	if got := f.balance(t); got != "5000.00" {
		t.Errorf("a draft moved money: the farmer is at %s", got)
	}

	resp := request(t, f.handler, http.MethodPost, "/api/payments/"+itoa(draft.ID)+"/post", f.encoder, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("post: status %d: %s", resp.Code, resp.Body)
	}
	var posted payment.Payment
	decode(t, resp, &posted)

	if posted.Status != payment.StatusPosted || posted.OrNo != 1 {
		t.Errorf("the posted payment is %s with OR No. %d, want posted and numbered", posted.Status, posted.OrNo)
	}
	if got := f.balance(t); got != "3000.00" {
		t.Errorf("the farmer is at %s after paying ₱2,000.00 of ₱5,000.00", got)
	}

	// The detail screen reads the split back off the entry, which is the only
	// place it is stored.
	var detail paymentDetail
	decode(t, request(t, f.handler, http.MethodGet, "/api/payments/"+itoa(draft.ID), f.encoder, nil), &detail)
	if len(detail.Entries) != 1 {
		t.Fatalf("the posted payment shows %d entries, want one", len(detail.Entries))
	}
	if detail.Entries[0].Principal != "-2000.00" || detail.Entries[0].Interest != "0.00" {
		t.Errorf("the entry moved %s of principal and %s of interest",
			detail.Entries[0].Principal, detail.Entries[0].Interest)
	}
	if detail.Balance.Principal != "3000.00" {
		t.Errorf("the screen shows a balance of %s beside the receipt", detail.Balance.Principal)
	}
}

func TestPostingTwiceIsAConflictNotASecondCredit(t *testing.T) {
	f := newPaymentFixture(t)
	draft := f.create(t, f.draft("2000.00"))

	first := request(t, f.handler, http.MethodPost, "/api/payments/"+itoa(draft.ID)+"/post", f.encoder, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("post: status %d: %s", first.Code, first.Body)
	}
	second := request(t, f.handler, http.MethodPost, "/api/payments/"+itoa(draft.ID)+"/post", f.encoder, nil)
	if second.Code != http.StatusConflict {
		t.Errorf("the second post returned %d, want 409", second.Code)
	}
	if got := f.balance(t); got != "3000.00" {
		t.Errorf("the farmer is at %s after a double-clicked Post, want 3000.00", got)
	}
}

func TestOnlyAManagerCanVoidAPayment(t *testing.T) {
	f := newPaymentFixture(t)

	draft := f.create(t, f.draft("2000.00"))
	if resp := request(t, f.handler, http.MethodPost, "/api/payments/"+itoa(draft.ID)+"/post",
		f.encoder, nil); resp.Code != http.StatusOK {
		t.Fatalf("post: status %d: %s", resp.Code, resp.Body)
	}

	refused := request(t, f.handler, http.MethodPost, "/api/payments/"+itoa(draft.ID)+"/void",
		f.encoder, voidRequest{Reason: "receipt written twice"})
	if refused.Code != http.StatusForbidden {
		t.Fatalf("an encoder's void returned %d, want 403", refused.Code)
	}
	if got := f.balance(t); got != "3000.00" {
		t.Errorf("a refused void moved money: the farmer is at %s", got)
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
	if resp := request(t, f.handler, http.MethodPost, "/api/payments/"+itoa(draft.ID)+"/void",
		f.manager, voidRequest{}); resp.Code != http.StatusBadRequest {
		t.Errorf("voiding with no reason returned %d, want 400", resp.Code)
	}

	ok := request(t, f.handler, http.MethodPost, "/api/payments/"+itoa(draft.ID)+"/void",
		f.manager, voidRequest{Reason: "receipt written twice"})
	if ok.Code != http.StatusOK {
		t.Fatalf("the manager's void returned %d: %s", ok.Code, ok.Body)
	}
	if got := f.balance(t); got != "5000.00" {
		t.Errorf("the farmer is at %s after the payment was voided, want the 5000.00 they owed", got)
	}
}

// A caller cannot hand itself a number or a status, even by trying: Draft
// carries neither field, and decodeJSON refuses what it does not know.
func TestAPaymentCannotBeGivenItsOwnNumberOrStatus(t *testing.T) {
	f := newPaymentFixture(t)

	for _, field := range []string{"or_no", "status"} {
		body := f.draft("100.00")
		body[field] = "posted"
		if field == "or_no" {
			body[field] = 999
		}

		resp := request(t, f.handler, http.MethodPost, "/api/payments", f.encoder, body)
		if resp.Code != http.StatusBadRequest {
			t.Errorf("a payment supplying %s returned %d, want 400: %s", field, resp.Code, resp.Body)
		}
	}
}

func TestADraftPaymentIsEditedAndDeleted(t *testing.T) {
	f := newPaymentFixture(t)
	draft := f.create(t, f.draft("2000.00"))

	edited := f.draft("2500.00")
	edited["method"] = "palay"
	resp := request(t, f.handler, http.MethodPut, "/api/payments/"+itoa(draft.ID), f.encoder, edited)
	if resp.Code != http.StatusOK {
		t.Fatalf("edit: status %d: %s", resp.Code, resp.Body)
	}

	gone := request(t, f.handler, http.MethodDelete, "/api/payments/"+itoa(draft.ID), f.encoder, nil)
	if gone.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d: %s", gone.Code, gone.Body)
	}

	// What it said survives where a deleted draft should be looked for.
	var before string
	if err := f.db.QueryRow(
		`SELECT before_json FROM audit_log WHERE action = 'payment.draft.deleted'`).Scan(&before); err != nil {
		t.Fatalf("read the audit entry for a deleted draft: %v", err)
	}
	if before == "" || before == "null" {
		t.Error("the deleted draft's contents are not in the audit log")
	}
}

// The statement is the screen a farmer is shown. It must open where the
// previous one closed, print the documents by the numbers staff say out loud,
// and close where the rest of the system says the balance is.
func TestTheStatementShowsBothDocumentsByTheirNumbers(t *testing.T) {
	f := newPaymentFixture(t)

	paid := f.create(t, f.draft("2000.00"))
	if resp := request(t, f.handler, http.MethodPost, "/api/payments/"+itoa(paid.ID)+"/post",
		f.encoder, nil); resp.Code != http.StatusOK {
		t.Fatalf("post: status %d: %s", resp.Code, resp.Body)
	}

	var stmt statementResponse
	resp := request(t, f.handler, http.MethodGet,
		"/api/customers/"+itoa(f.farmer.ID)+"/statement", f.encoder, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("statement: status %d: %s", resp.Code, resp.Body)
	}
	decode(t, resp, &stmt)

	if len(stmt.Rows) != 2 {
		t.Fatalf("the statement has %d rows, want the advance and the payment", len(stmt.Rows))
	}
	if stmt.Rows[0].Document != "Ref. No. 1" {
		t.Errorf("the advance is labelled %q, want \"Ref. No. 1\"", stmt.Rows[0].Document)
	}
	if stmt.Rows[1].Document != "OR No. 1" {
		t.Errorf("the payment is labelled %q, want \"OR No. 1\"", stmt.Rows[1].Document)
	}
	if stmt.Rows[0].Running.Principal != "5000.00" || stmt.Rows[1].Running.Principal != "3000.00" {
		t.Errorf("the running balance goes %s then %s, want 5000.00 then 3000.00",
			stmt.Rows[0].Running.Principal, stmt.Rows[1].Running.Principal)
	}
	if stmt.Closing.Principal != f.balance(t) {
		t.Errorf("the statement closes at %s and the customer screen says %s",
			stmt.Closing.Principal, f.balance(t))
	}
	if stmt.Moved.Principal != "3000.00" {
		t.Errorf("the period moved %s, want 3000.00 — 5000.00 lent less 2000.00 paid", stmt.Moved.Principal)
	}
	if stmt.Customer.ID != f.farmer.ID {
		t.Error("the statement does not say whose it is")
	}
}

func TestAStatementOfSomebodyNotOnTheBooks(t *testing.T) {
	f := newPaymentFixture(t)

	resp := request(t, f.handler, http.MethodGet, "/api/customers/9999/statement", f.encoder, nil)
	if resp.Code != http.StatusNotFound {
		t.Errorf("a statement of a stranger returned %d, want 404", resp.Code)
	}
}
