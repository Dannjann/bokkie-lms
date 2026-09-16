package interest

import (
	"fmt"

	"github.com/Dannjann/bokkie-lms/internal/domain/money"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// The rule: what interest is charged on, and how much.
//
// This is Q1 of the domain interview, which is unanswered, and the whole of
// what the answer decides is in this file. Everything else in the package — the
// run, the preview, the numbering, the reversal — is right under any of the
// likely answers, which is what made it reasonable to build the step before the
// question came back.
//
// Two answers would still cost code rather than a setting, and they are worth
// naming where somebody implementing the third will see them: interest
// pro-rated when a farmer repays early, which is a negative interest movement
// nothing here computes, and a rate negotiated per customer, which would put a
// column on customers and a second argument on Charge.

// Method is how interest is computed, if at all.
type Method string

const (
	// MethodNone charges nothing, and is the default a fresh install starts
	// with. A run cannot be created while it is set: the refusal is a sentence
	// a manager reads, where a run full of zero charges would be a screen
	// nobody questions.
	MethodNone Method = "none"

	// MethodMonthlyOnBalance charges the rate against what each farmer owed at
	// the end of the period.
	MethodMonthlyOnBalance Method = "monthly-on-balance"

	// MethodFlatOnAdvance charges the rate, once, against what each farmer was
	// advanced during the period — interest as a fee on the loan rather than a
	// charge on a balance that is still outstanding.
	MethodFlatOnAdvance Method = "flat-on-advance"
)

// Methods is every method, in the order a settings screen should offer them.
var Methods = []Method{MethodNone, MethodMonthlyOnBalance, MethodFlatOnAdvance}

// Valid reports whether m is one of the three.
func (m Method) Valid() bool {
	for _, known := range Methods {
		if m == known {
			return true
		}
	}
	return false
}

func (m Method) String() string { return string(m) }

// Basis is what "what they owe" means when interest is charged on a balance.
type Basis string

const (
	// BasisPrincipal charges on principal alone: simple interest, and the
	// default.
	BasisPrincipal Basis = "principal"

	// BasisPrincipalAndInterest charges on principal plus interest already
	// charged and not yet paid — compounding.
	BasisPrincipalAndInterest Basis = "principal-and-interest"
)

// Bases is both, in the order a settings screen should offer them.
var Bases = []Basis{BasisPrincipal, BasisPrincipalAndInterest}

// Valid reports whether b is one of the two.
func (b Basis) Valid() bool {
	return b == BasisPrincipal || b == BasisPrincipalAndInterest
}

func (b Basis) String() string { return string(b) }

// Rule is the whole of the station's interest policy.
//
// A run carries a copy of it rather than a reference to the settings it came
// from. A run opened in two years has to be able to say what produced it, and
// by then the setting will have moved.
type Rule struct {
	Method Method `json:"method"`

	// RateBps is the rate in basis points: 300 is 3%, 25 is a quarter of one
	// percent. An integer against a denominator of 10,000 rather than a
	// decimal, because a rate written as 3.5 is a float waiting to happen and
	// internal/domain/money fails the build over one.
	RateBps int64 `json:"rate_bps"`

	Basis Basis `json:"basis"`
}

// denominator is what a basis point is a part of.
const denominator = 10_000

// Validate reports why this rule cannot produce a run, in words a manager can
// act on — every one of them names the setting to go and change.
func (r Rule) Validate() error {
	switch {
	case r.Method == MethodNone:
		return ValidationError{
			Field:   settings.InterestMethod.Key,
			Message: "no interest rule is set — choose how interest is computed in Settings before running it",
		}
	case !r.Method.Valid():
		return ValidationError{
			Field:   settings.InterestMethod.Key,
			Message: fmt.Sprintf("%q is not a way interest is computed here", r.Method),
		}
	case r.RateBps <= 0:
		return ValidationError{
			Field:   settings.InterestRateBps.Key,
			Message: "the interest rate is zero — set it in Settings before running interest",
		}
	case !r.Basis.Valid():
		return ValidationError{
			Field:   settings.InterestBasis.Key,
			Message: fmt.Sprintf("%q is not something interest is charged on", r.Basis),
		}
	}
	return nil
}

// Charge is the rate applied to a base, and it is the whole of the arithmetic
// this step exists to get right.
//
// Pure, exported and taking no context, so the property tests can hammer it
// directly and a reader can check it against a calculator.
//
//	charge = base × rate_bps ÷ 10,000, rounded half-up
//
// Half-up because it is what a person does with a pencil, and every one of
// these figures may be checked by a farmer with one. money.Rounding has no
// zero value precisely so that this is a decision somebody made rather than a
// property of the language.
//
// MulDiv multiplies at full width before dividing, so the result is the exact
// rounding of the exact quotient rather than the rounding of an already rounded
// step — and it cannot overflow on the way through.
//
// **A base that is zero or negative charges nothing.** A farmer in credit is
// not charged interest on the credit, and the clamp lives here rather than only
// in the caller for the reason payment.Split clamps rather than trusting its
// caller: an unclamped rate against a negative base is a negative charge, which
// the ledger would refuse — after a preview had already shown it to somebody.
func Charge(base money.Money, rule Rule) (money.Money, error) {
	if err := rule.Validate(); err != nil {
		return money.Zero, err
	}
	if !base.IsPositive() {
		return money.Zero, nil
	}

	charge, err := base.MulDiv(rule.RateBps, denominator, money.RoundHalfUp)
	if err != nil {
		return money.Zero, fmt.Errorf("charge %d bps on %s: %w", rule.RateBps, base, err)
	}
	return charge, nil
}

// RuleFor reads the station's rule out of its settings.
//
// It is read once when a draft run is created and copied onto the run, never
// consulted again for that run. Changing the rate does not change a draft that
// already exists — which the screen has to say, or somebody will change the
// setting, look at an unchanged preview, and conclude the setting does nothing.
func RuleFor(st Station) (Rule, error) {
	rule := Rule{
		Method:  Method(st.Choice(settings.InterestMethod)),
		RateBps: int64(st.Int(settings.InterestRateBps)),
		Basis:   Basis(st.Choice(settings.InterestBasis)),
	}
	if err := rule.Validate(); err != nil {
		return Rule{}, err
	}
	return rule, nil
}
