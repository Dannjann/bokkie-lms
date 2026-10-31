package api

import (
	"database/sql"
	"net/http"
	"strings"
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

// The everyday path at the counter: a payment is typed and recorded. Recording
// it numbers it and does not move the balance — that waits for the settlement,
// which is the legacy rule (see settlements_test.go for the rest of the path).
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

	if posted.Status != payment.StatusPosted || posted.OrNo != 1 || posted.SettlementNo != 0 {
		t.Errorf("the recorded payment is %s with OR No. %d settled by %d, want recorded, numbered and unsettled",
			posted.Status, posted.OrNo, posted.SettlementNo)
	}
	if got := f.balance(t); got != "5000.00" {
		t.Errorf("recording a payment moved the balance to %s; it waits for the settlement", got)
	}

	var detail paymentDetail
	decode(t, request(t, f.handler, http.MethodGet, "/api/payments/"+itoa(draft.ID), f.encoder, nil), &detail)
	if len(detail.Entries) != 0 {
		t.Errorf("a recorded payment shows %d ledger entries, want none until it is settled", len(detail.Entries))
	}
}

func TestAPaymentCanBeRecordedWithoutStoppingAtADraft(t *testing.T) {
	f := newPaymentFixture(t)

	resp := request(t, f.handler, http.MethodPost, "/api/payments/posted", f.encoder, f.draft("2000.00"))
	if resp.Code != http.StatusCreated {
		t.Fatalf("record at once: status %d: %s", resp.Code, resp.Body)
	}
	var p payment.Payment
	decode(t, resp, &p)
	if p.Status != payment.StatusPosted || p.OrNo != 1 {
		t.Errorf("the payment is %s with OR No. %d, want recorded as 1", p.Status, p.OrNo)
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
	var orNos int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM payments WHERE or_no IS NOT NULL`).Scan(&orNos); err != nil {
		t.Fatal(err)
	}
	if orNos != 1 {
		t.Errorf("a double-clicked Post handed out %d OR Nos., want 1", orNos)
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
	if got := f.balance(t); got != "5000.00" {
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

// The browser sends an empty amount for palay, because the server adds up the
// lines. That body has to be read, not refused before the lines are looked at.
func TestAPalayPaymentNeedsNoAmountTyped(t *testing.T) {
	f := newPaymentFixture(t)

	body := f.draft("")
	body["method"] = "palay"
	body["lines"] = []map[string]any{{"class": "YCS", "sacks": 4, "kilos": "200.0", "price": "12.50"}}
	palay := f.create(t, body)
	if palay.Amount.String() != "2500.00" {
		t.Errorf("200 kg at 12.50 came to %s, want 2500.00", palay.Amount)
	}

	// A cash payment with nothing typed is still refused, as a field to fix
	// rather than as a request that could not be read.
	if resp := request(t, f.handler, http.MethodPost, "/api/payments", f.encoder, f.draft("")); resp.Code != http.StatusBadRequest ||
		strings.Contains(resp.Body.String(), "could not read") {
		t.Errorf("a cash payment with no amount: status %d: %s, want a 400 about the amount", resp.Code, resp.Body)
	}
}

func TestADraftPaymentIsEditedAndDeleted(t *testing.T) {
	f := newPaymentFixture(t)
	draft := f.create(t, f.draft("2000.00"))

	edited := f.draft("2500.00")
	edited["method"] = "palay"
	edited["lines"] = []map[string]any{{"class": "YCS", "sacks": 4, "kilos": "200.0", "price": "12.50"}}
	resp := request(t, f.handler, http.MethodPut, "/api/payments/"+itoa(draft.ID), f.encoder, edited)
	if resp.Code != http.StatusOK {
		t.Fatalf("edit: status %d: %s", resp.Code, resp.Body)
	}
	var palay payment.Payment
	decode(t, resp, &palay)
	if palay.Amount.String() != "2500.00" || len(palay.Lines) != 1 || palay.Lines[0].Cost.String() != "2500.00" {
		t.Errorf("200 kg at 12.50 came to %s over %+v, want 2500.00 on one line", palay.Amount, palay.Lines)
	}

	// A cash payment does not carry lines.
	cash := f.draft("100.00")
	cash["lines"] = edited["lines"]
	if resp := request(t, f.handler, http.MethodPut, "/api/payments/"+itoa(draft.ID), f.encoder, cash); resp.Code != http.StatusBadRequest {
		t.Errorf("a cash payment with palay lines: status %d, want 400", resp.Code)
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

	// A payment reaches the statement when it is settled.
	var st struct {
		ID int64 `json:"id"`
	}
	created := request(t, f.handler, http.MethodPost, "/api/settlements", f.encoder,
		map[string]any{"customer_id": f.farmer.ID, "cutoff": today(t)})
	if created.Code != http.StatusCreated {
		t.Fatalf("settle: status %d: %s", created.Code, created.Body)
	}
	decode(t, created, &st)
	if resp := request(t, f.handler, http.MethodPost, "/api/settlements/"+itoa(st.ID)+"/post",
		f.encoder, nil); resp.Code != http.StatusOK {
		t.Fatalf("post the settlement: status %d: %s", resp.Code, resp.Body)
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
	if stmt.Rows[0].Document != "Ref. No. 1426" {
		t.Errorf("the advance is labelled %q, want \"Ref. No. 1426\"", stmt.Rows[0].Document)
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

// A statement that cannot be built is a fault, and must not be reported as a
// bad date range — that is what sends somebody looking at the date pickers
// while the real error sits unlogged. Only a range running backwards is the
// caller's mistake.
func TestABadStatementRangeAndABrokenOneAreDifferentAnswers(t *testing.T) {
	f := newPaymentFixture(t)
	statement := "/api/customers/" + itoa(f.farmer.ID) + "/statement"

	backwards := request(t, f.handler, http.MethodGet,
		statement+"?from=2026-03-31&to=2026-03-01", f.encoder, nil)
	if backwards.Code != http.StatusBadRequest {
		t.Errorf("a statement running backwards returned %d, want 400", backwards.Code)
	}

	// A row written by something other than this system — a hand repair in a
	// SQLite browser is the realistic case — with a timestamp the ledger cannot
	// read. The statement cannot be built, and that is a 500.
	if _, err := f.db.Exec(
		`INSERT INTO ledger_entries (customer_id, entry_date, entry_type,
		     principal_delta_cents, interest_delta_cents, source_table, reason,
		     created_at, created_by_username)
		 VALUES (?, '2026-03-02', 'interest', 0, 60000, '', '', 'yesterday afternoon', 'console')`,
		f.farmer.ID); err != nil {
		t.Fatalf("plant an unreadable entry: %v", err)
	}

	broken := request(t, f.handler, http.MethodGet, statement, f.encoder, nil)
	if broken.Code != http.StatusInternalServerError {
		t.Errorf("a statement over an unreadable entry returned %d, want 500: %s", broken.Code, broken.Body)
	}
}
