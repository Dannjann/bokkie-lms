package settlement

import (
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// Rows taken from the legacy ADVTRAN table, where every liquidated advance
// stores its principal, rate, days and the interest the old program charged.
// All 7,072 of them match this formula; these are the ones that would catch a
// wrong rounding mode or a wrong month — including two that land exactly on a
// half centavo, which the legacy program rounded up.
func TestChargeReproducesTheLegacyProgram(t *testing.T) {
	cases := []struct {
		principal int64 // centavos
		rateBps   int64
		days      int
		want      int64 // centavos, as the legacy program stored it
	}{
		{150000, 500, 17, 4250},
		{82500, 500, 87, 11963},  // 119.625 → 119.63
		{82000, 300, 690, 56580}, // nearly two years at 3%
		{17000, 800, 59, 2675},   // 26.7466… → 26.75
		{46500, 300, 847, 39386}, // 393.855 → 393.86
		{1000000, 500, 90, 150000},
		{1000000, 500, 30, 50000},
	}
	for _, tc := range cases {
		got, err := Charge(money.FromCentavos(tc.principal), tc.rateBps, tc.days)
		if err != nil {
			t.Fatalf("charge: %v", err)
		}
		if got.Centavos() != tc.want {
			t.Errorf("%s at %d bps for %d days is %s, want %s",
				money.FromCentavos(tc.principal), tc.rateBps, tc.days, got, money.FromCentavos(tc.want))
		}
	}
}

// Nothing is charged on nothing, on a credit, for no days, or at no rate — the
// legacy data has two settlements at 0%, which is how a charge was waived.
func TestChargeIsZeroWhenItShouldBe(t *testing.T) {
	cases := []struct {
		principal int64
		rateBps   int64
		days      int
	}{
		{0, 500, 30},
		{-100000, 500, 30},
		{100000, 0, 30},
		{100000, 500, 0},
		{100000, 500, -3},
	}
	for _, tc := range cases {
		got, err := Charge(money.FromCentavos(tc.principal), tc.rateBps, tc.days)
		if err != nil {
			t.Fatalf("charge: %v", err)
		}
		if !got.IsZero() {
			t.Errorf("%s at %d bps for %d days charged %s, want nothing",
				money.FromCentavos(tc.principal), tc.rateBps, tc.days, got)
		}
	}
}
