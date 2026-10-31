package api

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/auth"
	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/payment"
	"github.com/Dannjann/bokkie-lms/internal/domain/settlement"
)

type settlementFixture struct {
	deps    Deps
	handler *Handler
	db      *sql.DB

	encoder []*http.Cookie
	manager []*http.Cookie

	farmer customer.Customer
}

// daysAgo is a station date n days before today.
func daysAgo(t *testing.T, n int) string {
	t.Helper()
	d, err := ledger.ParseDate(today(t))
	if err != nil {
		t.Fatal(err)
	}
	return d.AddDays(-n).String()
}

// newSettlementFixture is a station with one farmer who borrowed ₱10,000.00
// ninety days ago, through the real advance-slip route: the owner's own
// example, "a farmer borrows ₱10,000 and pays back three months later".
func newSettlementFixture(t *testing.T) *settlementFixture {
	t.Helper()

	database := openTestDB(t)
	deps := testDeps(t, database)
	handler := NewHandler(deps)

	seedUser(t, deps, "encoder", "counter-password", auth.RoleEncoder)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)

	f := &settlementFixture{
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

	f.advance(t, f.farmer.ID, daysAgo(t, 90), "10000.00")
	return f
}

func (f *settlementFixture) advance(t *testing.T, customerID int64, date, amount string) {
	t.Helper()
	created := request(t, f.handler, http.MethodPost, "/api/advances", f.encoder, map[string]any{
		"date": date, "notes": "",
		"lines": []map[string]any{{"customer_id": customerID, "particular": "2 BAGS UREA", "amount": amount}},
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("seed an advance slip: status %d: %s", created.Code, created.Body)
	}
	var v struct {
		ID int64 `json:"id"`
	}
	decode(t, created, &v)
	if posted := request(t, f.handler, http.MethodPost, "/api/advances/"+itoa(v.ID)+"/post",
		f.encoder, nil); posted.Code != http.StatusOK {
		t.Fatalf("post the advance slip: status %d: %s", posted.Code, posted.Body)
	}
}

// deliver records a palay payment and returns it.
func (f *settlementFixture) deliver(t *testing.T, customerID int64, date, kilos, price string) payment.Payment {
	t.Helper()
	created := request(t, f.handler, http.MethodPost, "/api/payments", f.encoder, map[string]any{
		"customer_id": customerID, "date": date, "method": "palay", "particular": "",
		"lines": []map[string]any{{"class": "slg", "sacks": 10, "kilos": kilos, "price": price}},
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("record a delivery: status %d: %s", created.Code, created.Body)
	}
	var p payment.Payment
	decode(t, created, &p)
	posted := request(t, f.handler, http.MethodPost, "/api/payments/"+itoa(p.ID)+"/post", f.encoder, nil)
	if posted.Code != http.StatusOK {
		t.Fatalf("record the delivery: status %d: %s", posted.Code, posted.Body)
	}
	decode(t, posted, &p)
	return p
}

func (f *settlementFixture) settle(t *testing.T, body map[string]any) settlement.Settlement {
	t.Helper()
	resp := request(t, f.handler, http.MethodPost, "/api/settlements", f.encoder, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("compute a settlement: status %d: %s", resp.Code, resp.Body)
	}
	var st settlement.Settlement
	decode(t, resp, &st)
	return st
}

func (f *settlementFixture) post(t *testing.T, id int64) settlement.Settlement {
	t.Helper()
	resp := request(t, f.handler, http.MethodPost, "/api/settlements/"+itoa(id)+"/post", f.encoder, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("post settlement %d: status %d: %s", id, resp.Code, resp.Body)
	}
	var st settlement.Settlement
	decode(t, resp, &st)
	return st
}

func (f *settlementFixture) balance(t *testing.T, customerID int64) (principal, interestOwed string) {
	t.Helper()
	var detail customerDetail
	decode(t, request(t, f.handler, http.MethodGet, "/api/customers/"+itoa(customerID), f.manager, nil), &detail)
	return detail.Balance.Principal, detail.Balance.Interest
}

// The everyday path, end to end, in the owner's own numbers: ₱10,000 out for
// ninety days at 5% a month is ₱1,500 of interest. Five hundred kilos of palay
// at ₱14.00 is ₱7,000, which clears the interest first and ₱5,500 of the
// principal, leaving ₱4,500.
func TestAFarmerDeliversAndIsSettled(t *testing.T) {
	f := newSettlementFixture(t)

	p := f.deliver(t, f.farmer.ID, today(t), "500.0", "14.00")
	if p.Amount.String() != "7000.00" || len(p.Lines) != 1 || p.Lines[0].Class != "SLG" {
		t.Fatalf("the delivery came to %s with lines %+v, want 7000.00 on one SLG line", p.Amount, p.Lines)
	}
	if principal, _ := f.balance(t, f.farmer.ID); principal != "10000.00" {
		t.Errorf("recording a delivery moved the balance to %s; it waits for the settlement", principal)
	}

	st := f.settle(t, map[string]any{"customer_id": f.farmer.ID, "cutoff": today(t), "notes": ""})
	if st.Status != settlement.StatusDraft || st.RateBps != 500 {
		t.Fatalf("a new settlement is %s at %d bps, want a draft at the default 500", st.Status, st.RateBps)
	}
	if len(st.Advances) != 1 || st.Advances[0].Days != 90 || st.Advances[0].Charge.String() != "1500.00" {
		t.Fatalf("the preview charges %+v, want one advance of 90 days charged 1500.00", st.Advances)
	}
	if len(st.Payments) != 1 || st.Payments[0].Interest.String() != "1500.00" ||
		st.Payments[0].Principal.String() != "5500.00" {
		t.Fatalf("the preview applies %+v, want 1500.00 to interest and 5500.00 to principal", st.Payments)
	}
	if st.Totals.BalanceAfter.String() != "4500.00" {
		t.Errorf("the preview leaves %s owing, want 4500.00", st.Totals.BalanceAfter)
	}

	posted := f.post(t, st.ID)
	if posted.Status != settlement.StatusPosted || posted.No != 1 {
		t.Fatalf("the posted settlement is %s number %d, want posted number 1", posted.Status, posted.No)
	}
	if principal, owed := f.balance(t, f.farmer.ID); principal != "4500.00" || owed != "0.00" {
		t.Errorf("after settling the farmer owes %s principal and %s interest, want 4500.00 and 0.00", principal, owed)
	}

	var detail paymentDetail
	decode(t, request(t, f.handler, http.MethodGet, "/api/payments/"+itoa(p.ID), f.encoder, nil), &detail)
	if detail.Payment.SettlementNo != 1 {
		t.Errorf("the payment says it was settled by %d, want settlement 1", detail.Payment.SettlementNo)
	}

	// A settled payment cannot be voided on its own.
	resp := request(t, f.handler, http.MethodPost, "/api/payments/"+itoa(p.ID)+"/void", f.manager,
		map[string]string{"reason": "wrong farmer"})
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "void the liquidation first") {
		t.Errorf("voiding a settled payment: status %d: %s, want 409 naming the settlement", resp.Code, resp.Body)
	}

	// Nor can the advance it charged interest on.
	var slips struct {
		Vouchers []struct {
			ID int64 `json:"id"`
		} `json:"vouchers"`
	}
	decode(t, request(t, f.handler, http.MethodGet, "/api/advances", f.manager, nil), &slips)
	resp = request(t, f.handler, http.MethodPost, "/api/advances/"+itoa(slips.Vouchers[0].ID)+"/void", f.manager,
		map[string]string{"reason": "typed twice"})
	if resp.Code != http.StatusConflict {
		t.Errorf("voiding a settled advance slip: status %d: %s, want 409", resp.Code, resp.Body)
	}

	// Voiding is a manager's.
	resp = request(t, f.handler, http.MethodPost, "/api/settlements/"+itoa(st.ID)+"/void", f.encoder,
		map[string]string{"reason": "wrong rate"})
	if resp.Code != http.StatusForbidden {
		t.Errorf("an encoder voided a settlement: status %d", resp.Code)
	}
	resp = request(t, f.handler, http.MethodPost, "/api/settlements/"+itoa(st.ID)+"/void", f.manager,
		map[string]string{"reason": "wrong rate"})
	if resp.Code != http.StatusOK {
		t.Fatalf("void the settlement: status %d: %s", resp.Code, resp.Body)
	}
	if principal, owed := f.balance(t, f.farmer.ID); principal != "10000.00" || owed != "0.00" {
		t.Errorf("after the void the farmer owes %s and %s, want 10000.00 and 0.00 as before", principal, owed)
	}

	// The void reopened the payment, so it can be settled again — or voided.
	again := f.settle(t, map[string]any{"customer_id": f.farmer.ID, "cutoff": today(t), "rate_bps": 300})
	if len(again.Payments) != 1 || again.Advances[0].Charge.String() != "900.00" {
		t.Errorf("resettling at 3%% charges %+v and applies %d payments, want 900.00 and the reopened payment",
			again.Advances, len(again.Payments))
	}
	resp = request(t, f.handler, http.MethodPost, "/api/payments/"+itoa(p.ID)+"/void", f.manager,
		map[string]string{"reason": "wrong farmer"})
	if resp.Code != http.StatusOK {
		t.Errorf("voiding a reopened payment: status %d: %s", resp.Code, resp.Body)
	}
}

// A payment recorded between the preview and the post changes the figures,
// and the post says so rather than writing different ones.
func TestASettlementRefusesAStalePreview(t *testing.T) {
	f := newSettlementFixture(t)
	f.deliver(t, f.farmer.ID, today(t), "100.0", "15.00")

	st := f.settle(t, map[string]any{"customer_id": f.farmer.ID, "cutoff": today(t)})
	f.deliver(t, f.farmer.ID, today(t), "100.0", "15.00")

	resp := request(t, f.handler, http.MethodPost, "/api/settlements/"+itoa(st.ID)+"/post", f.encoder, nil)
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "recompute") {
		t.Fatalf("posting a stale preview: status %d: %s, want 409 asking for a recompute", resp.Code, resp.Body)
	}
	if principal, _ := f.balance(t, f.farmer.ID); principal != "10000.00" {
		t.Errorf("a refused post moved the balance to %s", principal)
	}

	resp = request(t, f.handler, http.MethodPut, "/api/settlements/"+itoa(st.ID), f.encoder,
		map[string]any{"customer_id": f.farmer.ID, "cutoff": today(t)})
	if resp.Code != http.StatusOK {
		t.Fatalf("recompute: status %d: %s", resp.Code, resp.Body)
	}
	if posted := f.post(t, st.ID); len(posted.Payments) != 2 {
		t.Errorf("the recomputed settlement applied %d payments, want both", len(posted.Payments))
	}
}

// A settlement is what happens when a farmer pays. The preview can be shown
// without one — "how much do I owe today?" — but it cannot be posted.
func TestASettlementWithoutAPaymentCannotBePosted(t *testing.T) {
	f := newSettlementFixture(t)
	st := f.settle(t, map[string]any{"customer_id": f.farmer.ID, "cutoff": today(t)})
	if st.Totals.Owed.String() != "11500.00" {
		t.Errorf("the preview says %s is owed, want 11500.00", st.Totals.Owed)
	}

	resp := request(t, f.handler, http.MethodPost, "/api/settlements/"+itoa(st.ID)+"/post", f.encoder, nil)
	if resp.Code != http.StatusConflict {
		t.Errorf("posting with no payment: status %d: %s, want 409", resp.Code, resp.Body)
	}
}

// What one settlement leaves is carried into the next, charged from the first
// cut-off — and the first cannot be voided while the second stands.
func TestASecondSettlementChargesTheCarriedPrincipal(t *testing.T) {
	f := newSettlementFixture(t)
	first := daysAgo(t, 30)

	// 60 days at 5% on 10,000 is 1,000. 7,000 clears it and 6,000 of principal.
	f.deliver(t, f.farmer.ID, first, "500.0", "14.00")
	one := f.post(t, f.settle(t, map[string]any{"customer_id": f.farmer.ID, "cutoff": first}).ID)
	if one.Totals.PrincipalAfter.String() != "4000.00" {
		t.Fatalf("the first settlement left %s, want 4000.00", one.Totals.PrincipalAfter)
	}

	// 4,000 carried for 30 days at 5% is 200.
	f.deliver(t, f.farmer.ID, today(t), "100.0", "10.00")
	two := f.settle(t, map[string]any{"customer_id": f.farmer.ID, "cutoff": today(t)})
	if two.Carried.Principal.String() != "4000.00" || two.Carried.Days != 30 || two.Carried.Charge.String() != "200.00" {
		t.Fatalf("the second settlement carried %+v, want 4000.00 for 30 days charged 200.00", two.Carried)
	}
	if len(two.Advances) != 0 {
		t.Errorf("the second settlement charged %d advances again; the first settled them", len(two.Advances))
	}
	f.post(t, two.ID)

	resp := request(t, f.handler, http.MethodPost, "/api/settlements/"+itoa(one.ID)+"/void", f.manager,
		map[string]string{"reason": "wrong rate"})
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "later liquidation") {
		t.Errorf("voiding an earlier settlement: status %d: %s, want 409", resp.Code, resp.Body)
	}

	// A cut-off before the last one is refused.
	resp = request(t, f.handler, http.MethodPost, "/api/settlements", f.encoder,
		map[string]any{"customer_id": f.farmer.ID, "cutoff": daysAgo(t, 40)})
	if resp.Code != http.StatusBadRequest {
		t.Errorf("settling back before the last cut-off: status %d: %s, want 400", resp.Code, resp.Body)
	}

	// The statement names the settlement on its interest entry.
	stmt := request(t, f.handler, http.MethodGet, "/api/customers/"+itoa(f.farmer.ID)+"/statement", f.encoder, nil)
	if !strings.Contains(stmt.Body.String(), "Settlement No. 2") {
		t.Errorf("the statement does not name the settlement: %s", stmt.Body)
	}
}

// Unpaid interest is carried forward and owed, and never itself charged
// interest: the legacy system did not compound, and every one of its 231
// carried-forward charges was on principal alone.
func TestUnpaidInterestIsCarriedButNotCharged(t *testing.T) {
	f := newSettlementFixture(t)
	first := daysAgo(t, 30)

	// 60 days on 10,000 is 1,000 of interest; a 400 delivery leaves 600 of it unpaid.
	f.deliver(t, f.farmer.ID, first, "40.0", "10.00")
	one := f.post(t, f.settle(t, map[string]any{"customer_id": f.farmer.ID, "cutoff": first}).ID)
	if one.Totals.InterestAfter.String() != "600.00" || one.Totals.PrincipalAfter.String() != "10000.00" {
		t.Fatalf("the first settlement left %s interest and %s principal, want 600.00 and 10000.00",
			one.Totals.InterestAfter, one.Totals.PrincipalAfter)
	}

	f.deliver(t, f.farmer.ID, today(t), "10.0", "10.00")
	two := f.settle(t, map[string]any{"customer_id": f.farmer.ID, "cutoff": today(t)})
	// 30 days on the 10,000 principal is 500 — not on 10,600.
	if two.Carried.Interest.String() != "600.00" || two.Carried.Charge.String() != "500.00" {
		t.Errorf("carried %s of interest and charged %s, want 600.00 carried and 500.00 charged on principal only",
			two.Carried.Interest, two.Carried.Charge)
	}
}

// A delivery worth more than the debt leaves the farmer in credit, and a
// credit is charged nothing at the next settlement.
func TestACreditIsNotCharged(t *testing.T) {
	f := newSettlementFixture(t)
	first := daysAgo(t, 30)

	// 11,000 owed (10,000 + 60 days' interest of 1,000); 12,000 delivered.
	f.deliver(t, f.farmer.ID, first, "1000.0", "12.00")
	one := f.post(t, f.settle(t, map[string]any{"customer_id": f.farmer.ID, "cutoff": first}).ID)
	if one.Totals.BalanceAfter.String() != "-1000.00" {
		t.Fatalf("the first settlement left %s, want a credit of -1000.00", one.Totals.BalanceAfter)
	}

	f.deliver(t, f.farmer.ID, today(t), "10.0", "10.00")
	two := f.settle(t, map[string]any{"customer_id": f.farmer.ID, "cutoff": today(t)})
	if two.Carried.Principal.String() != "-1000.00" || !two.Carried.Charge.IsZero() {
		t.Errorf("carried %s charged %s, want a credit of -1000.00 charged nothing",
			two.Carried.Principal, two.Carried.Charge)
	}
}
