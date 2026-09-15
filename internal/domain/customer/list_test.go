package customer

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// seedForSearch puts a small, deliberately awkward master in place: a literal
// percent sign, an underscore, a phone number, and one deactivated customer.
func seedForSearch(t *testing.T) *Service {
	t.Helper()
	svc, _ := openTestService(t)
	ctx := context.Background()

	drafts := []Draft{
		{Name: "Juan dela Cruz", Address: "Pintor Gamu", CellNo: "0917-123-4567"},
		{Name: "Maria Santos", Address: "Gamu", CellNo: "0918 765 4321"},
		{Name: "100% Palay Trading", Address: "Ilagan"},
		{Name: "Under_score Farms", Address: "Naguilian"},
		{Name: "Pedro Reyes", Address: "Gamu", CellNo: "+63 917 000 1111"},
	}
	for _, d := range drafts {
		create(t, svc, d)
	}

	// The last one is deactivated, so the default listing must not show it.
	if _, err := inTx(ctx, svc, func(tx *sql.Tx) (Customer, error) {
		return svc.SetActiveTx(ctx, tx, 5, false)
	}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	return svc
}

func names(page Page) []string {
	out := make([]string, len(page.Customers))
	for i, c := range page.Customers {
		out[i] = c.Name
	}
	return out
}

// Somebody typing % into a search box to see what happens must not be shown the
// entire master. Without an ESCAPE clause this returns everyone and looks like
// a broken filter.
func TestSearchingForAPercentSignIsLiteral(t *testing.T) {
	svc := seedForSearch(t)

	page, err := svc.List(context.Background(), Query{Text: "%"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("searching %%%% returned %d customers (%v), want just the one with a percent in its name",
			page.Total, names(page))
	}
	if page.Customers[0].Name != "100% Palay Trading" {
		t.Errorf("got %q", page.Customers[0].Name)
	}
}

func TestSearchingForAnUnderscoreIsLiteral(t *testing.T) {
	svc := seedForSearch(t)

	page, err := svc.List(context.Background(), Query{Text: "_"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("searching _ returned %d customers (%v)", page.Total, names(page))
	}
}

// The stored number is digits only, so a search has to be reduced the same way
// the number was before it can match.
func TestSearchingAPhoneNumberIgnoresPunctuation(t *testing.T) {
	svc := seedForSearch(t)
	ctx := context.Background()

	for _, q := range []string{"0917", "0917-123", "0917 123 4567", "09171234567"} {
		page, err := svc.List(ctx, Query{Text: q})
		if err != nil {
			t.Fatalf("list %q: %v", q, err)
		}
		if page.Total == 0 {
			t.Errorf("searching %q found nobody", q)
			continue
		}
		if page.Customers[0].Name != "Juan dela Cruz" {
			t.Errorf("searching %q found %q first", q, page.Customers[0].Name)
		}
	}
}

func TestSearchMatchesCodeAndName(t *testing.T) {
	svc := seedForSearch(t)
	ctx := context.Background()

	byCode, err := svc.List(ctx, Query{Text: "BA-0002"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if byCode.Total != 1 || byCode.Customers[0].Name != "Maria Santos" {
		t.Errorf("code search returned %v", names(byCode))
	}

	byName, err := svc.List(ctx, Query{Text: "santos"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if byName.Total != 1 {
		t.Errorf("name search returned %v", names(byName))
	}
}

// The everyday question at a counter is about somebody currently on the books.
func TestListingDefaultsToActiveCustomers(t *testing.T) {
	svc := seedForSearch(t)
	ctx := context.Background()

	active, err := svc.List(ctx, Query{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if active.Total != 4 {
		t.Errorf("default listing has %d customers (%v), want 4", active.Total, names(active))
	}

	inactive, err := svc.List(ctx, Query{Status: StatusInactive})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if inactive.Total != 1 || inactive.Customers[0].Name != "Pedro Reyes" {
		t.Errorf("inactive listing returned %v", names(inactive))
	}

	all, err := svc.List(ctx, Query{Status: StatusAny})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if all.Total != 5 {
		t.Errorf("full listing has %d customers, want 5", all.Total)
	}
}

// ORDER BY code is ORDER BY the sequence, because the format is zero-padded to
// a fixed width. Pagination is only meaningful if that holds.
func TestPaginationWalksTheWholeMasterInCodeOrder(t *testing.T) {
	svc, _ := openTestService(t)
	ctx := context.Background()

	for i := 1; i <= 25; i++ {
		create(t, svc, Draft{Name: fmt.Sprintf("Farmer %02d", i)})
	}

	var seen []string
	for page := 1; ; page++ {
		got, err := svc.List(ctx, Query{Page: page, PerPage: 10})
		if err != nil {
			t.Fatalf("list page %d: %v", page, err)
		}
		if got.Total != 25 {
			t.Fatalf("page %d reports a total of %d, want 25", page, got.Total)
		}
		if len(got.Customers) == 0 {
			break
		}
		for _, c := range got.Customers {
			seen = append(seen, c.Code)
		}
		if page > 5 {
			t.Fatal("pagination did not terminate")
		}
	}

	if len(seen) != 25 {
		t.Fatalf("walked %d customers, want 25", len(seen))
	}
	for i := 1; i < len(seen); i++ {
		if seen[i-1] >= seen[i] {
			t.Fatalf("codes are out of order at %d: %s then %s", i, seen[i-1], seen[i])
		}
	}
}

func TestPerPageIsClamped(t *testing.T) {
	svc, _ := openTestService(t)

	page, err := svc.List(context.Background(), Query{PerPage: 100_000})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.PerPage != MaxPerPage {
		t.Errorf("per page is %d, want it clamped to %d", page.PerPage, MaxPerPage)
	}
}

// An empty result is an empty list, not a null — the SPA iterates it directly.
func TestAnEmptyResultIsAnEmptyList(t *testing.T) {
	svc, _ := openTestService(t)

	page, err := svc.List(context.Background(), Query{Text: "nobody"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Customers == nil {
		t.Error("Customers is nil, want an empty slice")
	}
}
