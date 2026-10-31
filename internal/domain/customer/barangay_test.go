package customer

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func createBarangay(t *testing.T, svc *Service, name, prefix string) Barangay {
	t.Helper()
	b, err := inTx(context.Background(), svc, func(tx *sql.Tx) (Barangay, error) {
		return svc.CreateBarangayTx(context.Background(), tx, BarangayDraft{Name: name, Prefix: prefix})
	})
	if err != nil {
		t.Fatalf("create barangay %s: %v", name, err)
	}
	return b
}

// The client's rule: each barangay has its own sequence, and the customers
// entered before barangays keep the BA codes on their slips.
func TestEachBarangayCountsItsOwnCodes(t *testing.T) {
	svc, _ := openTestService(t)

	legacy := create(t, svc, Draft{Name: "Before Barangays"})
	pintor := createBarangay(t, svc, "Pintor", "pi")
	camarag := createBarangay(t, svc, "Camarag", "CMG")

	got := []string{
		create(t, svc, Draft{Name: "A", BarangayID: pintor.ID}).Code,
		create(t, svc, Draft{Name: "B", BarangayID: camarag.ID}).Code,
		create(t, svc, Draft{Name: "C", BarangayID: pintor.ID}).Code,
	}
	want := []string{"PI-0001", "CMG-0001", "PI-0002"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("customer %d got %s, want %s", i, got[i], want[i])
		}
	}
	if legacy.Code != "BA-0001" || legacy.BarangayID != 0 {
		t.Errorf("the legacy customer is %s in barangay %d, want BA-0001 in none", legacy.Code, legacy.BarangayID)
	}

	// Once there are barangays to pick from, a new customer must pick one.
	_, err := inTx(context.Background(), svc, func(tx *sql.Tx) (Customer, error) {
		return svc.CreateTx(context.Background(), tx, Draft{Name: "D"})
	})
	var ve ValidationError
	if !errors.As(err, &ve) || ve.Field != "barangay_id" {
		t.Errorf("a customer with no barangay: %v, want a barangay_id validation error", err)
	}

	c := create(t, svc, Draft{Name: "E", BarangayID: camarag.ID})
	if c.BarangayID != camarag.ID || c.BarangayName != "Camarag" {
		t.Errorf("the new customer is in %d %q, want Camarag", c.BarangayID, c.BarangayName)
	}
}

// Moving a farmer to a barangay never re-codes them.
func TestSettingABarangayKeepsTheCode(t *testing.T) {
	svc, _ := openTestService(t)
	legacy := create(t, svc, Draft{Name: "Juan"})
	pintor := createBarangay(t, svc, "Pintor", "PI")

	moved, err := inTx(context.Background(), svc, func(tx *sql.Tx) (Customer, error) {
		return svc.UpdateTx(context.Background(), tx, legacy.ID, Draft{Name: "Juan", BarangayID: pintor.ID})
	})
	if err != nil {
		t.Fatal(err)
	}
	if moved.Code != "BA-0001" || moved.BarangayName != "Pintor" {
		t.Errorf("after the move: %s in %q, want BA-0001 in Pintor", moved.Code, moved.BarangayName)
	}

	_, err = inTx(context.Background(), svc, func(tx *sql.Tx) (Customer, error) {
		return svc.UpdateTx(context.Background(), tx, legacy.ID, Draft{Name: "Juan", BarangayID: 999})
	})
	var ve ValidationError
	if !errors.As(err, &ve) || ve.Field != "barangay_id" {
		t.Errorf("a barangay that does not exist: %v, want a barangay_id validation error", err)
	}
}

func TestARetiredBarangayIssuesNoCodes(t *testing.T) {
	svc, _ := openTestService(t)
	pintor := createBarangay(t, svc, "Pintor", "PI")
	if _, err := inTx(context.Background(), svc, func(tx *sql.Tx) (Barangay, error) {
		return svc.UpdateBarangayTx(context.Background(), tx, pintor.ID, BarangayDraft{Name: "Pintor", Prefix: "PI"})
	}); err != nil {
		t.Fatal(err)
	}

	_, err := inTx(context.Background(), svc, func(tx *sql.Tx) (Customer, error) {
		return svc.CreateTx(context.Background(), tx, Draft{Name: "A", BarangayID: pintor.ID})
	})
	var ve ValidationError
	if !errors.As(err, &ve) || ve.Field != "barangay_id" {
		t.Errorf("a customer in a retired barangay: %v, want a barangay_id validation error", err)
	}
}

// A prefix is free to fix until a code has been handed out under it.
func TestAPrefixIsFixedOnceUsed(t *testing.T) {
	svc, _ := openTestService(t)
	b := createBarangay(t, svc, "Pintor", "PN")

	update := func(prefix string) error {
		_, err := inTx(context.Background(), svc, func(tx *sql.Tx) (Barangay, error) {
			return svc.UpdateBarangayTx(context.Background(), tx, b.ID,
				BarangayDraft{Name: "Pintor", Prefix: prefix, Active: true})
		})
		return err
	}
	if err := update("PI"); err != nil {
		t.Fatalf("fixing an unused prefix: %v", err)
	}
	create(t, svc, Draft{Name: "A", BarangayID: b.ID})

	if err := update("PIN"); !errors.Is(err, ErrPrefixInUse) {
		t.Errorf("changing a used prefix: %v, want ErrPrefixInUse", err)
	}
	if err := update("PI"); err != nil {
		t.Errorf("renaming without touching the prefix: %v", err)
	}
}

func TestBarangayNamesAndPrefixesAreUnique(t *testing.T) {
	svc, _ := openTestService(t)
	createBarangay(t, svc, "Pintor", "PI")

	for _, d := range []BarangayDraft{{Name: "pintor", Prefix: "PX"}, {Name: "Other", Prefix: "PI"}, {Name: "X", Prefix: "P1"}} {
		_, err := inTx(context.Background(), svc, func(tx *sql.Tx) (Barangay, error) {
			return svc.CreateBarangayTx(context.Background(), tx, d)
		})
		var ve ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%+v: %v, want a validation error", d, err)
		}
	}
}
