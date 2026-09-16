package interest

import (
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// The rule is pure, so it is tested without a database at all. These are the
// cases somebody would have to reason about with a calculator, written down.
func TestCharge(t *testing.T) {
	rule := func(bps int64) Rule {
		return Rule{Method: MethodMonthlyOnBalance, RateBps: bps, Basis: BasisPrincipal}
	}

	cases := []struct {
		name string
		base int64 // centavos
		rule Rule
		want int64 // centavos
	}{
		{"three percent of a round thousand", 100_000, rule(300), 3_000},
		{"three percent of five thousand pesos", 500_000, rule(300), 15_000},
		{"a quarter of one percent", 1_000_000, rule(25), 2_500},
		{"a hundred percent", 100_000, rule(10_000), 100_000},

		// The rounding boundaries, and the reason RoundHalfUp is named at the
		// call rather than left to a default. Half of a centavo goes up, which
		// is what a person does with a pencil — RoundDown gives 0 here, and
		// RoundHalfEven gives 0 as well.
		{"exactly half a centavo", 5_000, rule(1), 1},
		{"exactly one and a half centavos", 15_000, rule(1), 2},
		{"just under half a centavo", 4_999, rule(1), 0},
		{"just over half a centavo", 5_001, rule(1), 1},

		// Nothing to charge on. Both are skipped before a line is written, but
		// the clamp lives here too: an unclamped rate against a negative base
		// is a negative charge, and the ledger would refuse it only after a
		// preview had shown it to somebody.
		{"a farmer who owes nothing", 0, rule(300), 0},
		{"a farmer in credit", -500_000, rule(300), 0},

		// A base small enough that the charge rounds away. The run drops the
		// line rather than previewing an entry the ledger would refuse.
		{"ten centavos at three percent", 10, rule(300), 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Charge(money.FromCentavos(tc.base), tc.rule)
			if err != nil {
				t.Fatalf("charge: %v", err)
			}
			if got.Centavos() != tc.want {
				t.Errorf("%d bps on %s = %s, want %s",
					tc.rule.RateBps, money.FromCentavos(tc.base), got, money.FromCentavos(tc.want))
			}
			if got.IsNegative() {
				t.Errorf("a negative charge: %s", got)
			}
		})
	}
}

func TestChargeRefusesARuleThatCannotCharge(t *testing.T) {
	cases := []struct {
		name string
		rule Rule
	}{
		{"no method at all", Rule{Basis: BasisPrincipal, RateBps: 300}},
		{"method none", Rule{Method: MethodNone, RateBps: 300, Basis: BasisPrincipal}},
		{"a method nobody defined", Rule{Method: "monthly", RateBps: 300, Basis: BasisPrincipal}},
		{"a rate of zero", Rule{Method: MethodMonthlyOnBalance, Basis: BasisPrincipal}},
		{"a negative rate", Rule{Method: MethodMonthlyOnBalance, RateBps: -300, Basis: BasisPrincipal}},
		{"a basis nobody defined", Rule{Method: MethodMonthlyOnBalance, RateBps: 300, Basis: "everything"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Charge(money.FromCentavos(100_000), tc.rule); err == nil {
				t.Fatal("got no error, want one")
			}
		})
	}
}

// Every refusal names the setting to go and change, because the person reading
// it is a manager on a screen rather than a developer in a log.
func TestEveryRefusalNamesTheSettingToChange(t *testing.T) {
	cases := []struct {
		rule  Rule
		field string
	}{
		{Rule{Method: MethodNone, RateBps: 300, Basis: BasisPrincipal}, "interest.method"},
		{Rule{Method: MethodMonthlyOnBalance, Basis: BasisPrincipal}, "interest.rate_bps"},
		{Rule{Method: MethodMonthlyOnBalance, RateBps: 300, Basis: "everything"}, "interest.basis"},
	}

	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			err := tc.rule.Validate()
			var invalid ValidationError
			if !asValidation(err, &invalid) {
				t.Fatalf("got %v, want a validation error", err)
			}
			if invalid.Field != tc.field {
				t.Errorf("the complaint names %q, want %q", invalid.Field, tc.field)
			}
		})
	}
}

func asValidation(err error, target *ValidationError) bool {
	v, ok := err.(ValidationError)
	if ok {
		*target = v
	}
	return ok
}
