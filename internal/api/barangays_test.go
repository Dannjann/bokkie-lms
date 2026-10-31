package api

import (
	"net/http"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/auth"
	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
)

func TestAManagerKeepsTheBarangaysAndCodesFollowThem(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "encoder", "counter-password", auth.RoleEncoder)
	seedUser(t, deps, "boss", "manager-password", auth.RoleManager)
	encoder := login(t, handler, "encoder", "counter-password")
	manager := login(t, handler, "boss", "manager-password")

	body := map[string]any{"name": "Pintor", "prefix": "PI"}
	if resp := request(t, handler, http.MethodPost, "/api/barangays", encoder, body); resp.Code != http.StatusForbidden {
		t.Errorf("an encoder adding a barangay: status %d, want 403", resp.Code)
	}
	resp := request(t, handler, http.MethodPost, "/api/barangays", manager, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("a manager adding a barangay: status %d: %s", resp.Code, resp.Body)
	}
	var pintor customer.Barangay
	decode(t, resp, &pintor)

	// The encoder at the counter picks it, and the code follows.
	resp = request(t, handler, http.MethodPost, "/api/customers", encoder,
		map[string]any{"name": "Juan dela Cruz", "barangay_id": pintor.ID})
	if resp.Code != http.StatusCreated {
		t.Fatalf("a customer in Pintor: status %d: %s", resp.Code, resp.Body)
	}
	var c customer.Customer
	decode(t, resp, &c)
	if c.Code != "PI-0001" || c.BarangayName != "Pintor" {
		t.Errorf("the customer is %s in %q, want PI-0001 in Pintor", c.Code, c.BarangayName)
	}

	// Once a code is out, the prefix stays.
	resp = request(t, handler, http.MethodPut, "/api/barangays/"+itoa(pintor.ID), manager,
		map[string]any{"name": "Pintor", "prefix": "PIN", "active": true})
	if resp.Code != http.StatusConflict {
		t.Errorf("changing a used prefix: status %d: %s, want 409", resp.Code, resp.Body)
	}

	var list []customer.Barangay
	decode(t, request(t, handler, http.MethodGet, "/api/barangays", encoder, nil), &list)
	if len(list) != 1 || list[0].Customers != 1 {
		t.Errorf("the list is %+v, want Pintor with one customer", list)
	}
}
