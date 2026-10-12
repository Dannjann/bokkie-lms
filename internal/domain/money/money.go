// Package money is the only way a peso amount is represented in bokkie.
//
// An amount is int64 centavos. Not a float, anywhere, for any reason: 0.1+0.2
// is not 0.3 in binary floating point, and a lending ledger that is out by a
// centavo is a ledger nobody can defend two seasons later. That ban is enforced
// by a test which parses this package's syntax tree rather than by a review
// convention — see float_ban_test.go. A convention is one careless commit from
// being forgotten; a failing build is not.
//
// Amounts marshal to JSON as a decimal *string* ("1234.50"), never as a number.
// JavaScript has exactly one numeric type and it is a float, so a number on the
// wire is an invitation to do arithmetic in the browser. The SPA formats and
// displays what it is given; every peso that is added, subtracted or divided is
// added, subtracted or divided here.
package money

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Money is a signed amount in centavos.
//
// It is a defined type over int64 rather than a struct so it costs nothing to
// pass around and stores as a plain SQLite INTEGER, but it is *not* an alias:
// an int64 cannot be assigned to a Money without saying so, which is what stops
// a row count or a customer id from being added to a balance by accident.
type Money int64

// Zero is what a customer owes before anything has happened to them.
const Zero Money = 0

const centavosPerPeso = 100

// The range is ±92 quadrillion pesos. Nothing this station does will approach
// it, which is exactly why a silent wrap would be so hard to diagnose: it can
// only arrive through a bug, and it would arrive looking like a plausible
// number rather than like a crash.
var (
	// ErrOverflow means the result does not fit in an int64 of centavos.
	ErrOverflow = errors.New("peso amount is out of range")

	// ErrDivideByZero is its own error because "divide a balance by the number
	// of instalments" with no instalments is a caller bug worth naming.
	ErrDivideByZero = errors.New("cannot divide a peso amount by zero")

	// ErrNoRounding is returned when a division is asked for without saying how
	// to round. See Rounding.
	ErrNoRounding = errors.New("a division of pesos must name its rounding mode")

	// ErrNotAnAmount means the text could not be read as a peso amount, or
	// could be read as more than one.
	ErrNotAnAmount = errors.New("not a peso amount")
)

// FromCentavos builds an amount from a raw centavo count — the form it takes in
// the database and on the wire between Go and SQLite.
func FromCentavos(c int64) Money { return Money(c) }

// FromPesos builds a whole-peso amount. It is for literals in tests and
// defaults, not for user input: use Parse for anything a person typed.
func FromPesos(p int64) (Money, error) {
	if p > math.MaxInt64/centavosPerPeso || p < math.MinInt64/centavosPerPeso {
		return 0, fmt.Errorf("%w: %d pesos", ErrOverflow, p)
	}
	return Money(p * centavosPerPeso), nil
}

// Centavos is the raw count, for storing and for tests. Nothing outside this
// package should be doing arithmetic on the result.
func (m Money) Centavos() int64 { return int64(m) }

// IsZero, IsNegative and IsPositive exist so callers compare through the type
// rather than reaching for Centavos() and comparing an int64 they then have
// lying around.
func (m Money) IsZero() bool     { return m == 0 }
func (m Money) IsNegative() bool { return m < 0 }
func (m Money) IsPositive() bool { return m > 0 }

// Add returns m+n, or an error if the sum would wrap.
func (m Money) Add(n Money) (Money, error) {
	sum := m + n
	// A wrap has happened exactly when both operands share a sign and the
	// result does not.
	if (m > 0 && n > 0 && sum < m) || (m < 0 && n < 0 && sum > m) {
		return 0, fmt.Errorf("%w: %s + %s", ErrOverflow, m, n)
	}
	return sum, nil
}

// Sub returns m−n, or an error if the difference would wrap.
func (m Money) Sub(n Money) (Money, error) {
	diff := m - n
	if (n < 0 && diff < m) || (n > 0 && diff > m) {
		return 0, fmt.Errorf("%w: %s - %s", ErrOverflow, m, n)
	}
	return diff, nil
}

// Neg returns −m. It can fail for exactly one value, the most negative int64,
// which has no positive counterpart.
func (m Money) Neg() (Money, error) {
	if m == math.MinInt64 {
		return 0, fmt.Errorf("%w: negating %s", ErrOverflow, m)
	}
	return -m, nil
}

// Sum adds any number of amounts, failing on the first wrap rather than
// returning a number that is quietly wrong.
func Sum(amounts ...Money) (Money, error) {
	total := Zero
	for _, a := range amounts {
		var err error
		if total, err = total.Add(a); err != nil {
			return 0, err
		}
	}
	return total, nil
}

// MulInt multiplies by a whole number — a count of days, a number of sacks.
// There is deliberately no Mul(Money, Money): pesos times pesos is not a
// quantity this domain has.
func (m Money) MulInt(n int64) (Money, error) {
	if m == 0 || n == 0 {
		return 0, nil
	}
	if m == math.MinInt64 && n == -1 {
		return 0, fmt.Errorf("%w: %s × %d", ErrOverflow, m, n)
	}
	product := int64(m) * n
	if product/n != int64(m) {
		return 0, fmt.Errorf("%w: %s × %d", ErrOverflow, m, n)
	}
	return Money(product), nil
}

// Rounding says what to do with the centavo fraction a division leaves behind.
//
// There is no default. Step 7's interest rule will divide, and "how did we
// round?" has to be a decision someone made and wrote down, not a property of
// whichever language the system happened to be written in. The zero value is
// invalid for exactly that reason.
type Rounding int

const (
	// RoundHalfUp sends a half away from zero: 0.5 → 1, −0.5 → −1. It is what a
	// person does with a pencil, which makes it the right default for anything
	// a farmer will check by hand.
	RoundHalfUp Rounding = iota + 1

	// RoundHalfEven sends a half to the nearest even centavo. Over many
	// roundings it does not drift upward the way half-up does, which is why
	// accounting systems reach for it on large aggregates.
	RoundHalfEven

	// RoundDown truncates toward zero — the lender's loss, the customer's gain.
	RoundDown

	// RoundUp goes away from zero on any remainder at all.
	RoundUp
)

func (r Rounding) valid() bool {
	return r >= RoundHalfUp && r <= RoundUp
}

func (r Rounding) String() string {
	switch r {
	case RoundHalfUp:
		return "half-up"
	case RoundHalfEven:
		return "half-even"
	case RoundDown:
		return "down"
	case RoundUp:
		return "up"
	default:
		return "unset"
	}
}

// Div divides an amount into equal parts, rounding as told.
func (m Money) Div(divisor int64, mode Rounding) (Money, error) {
	return m.MulDiv(1, divisor, mode)
}

// MulDiv returns m × num ÷ den in one step, rounded as told.
//
// This is how a rate is applied without ever writing one down as a float: three
// percent is MulDiv(3, 100), and two and a half percent is MulDiv(25, 1000).
// The multiplication happens at full width before the division, so the
// intermediate cannot overflow and the result is the exact rounding of the
// exact quotient — not the rounding of an already-rounded step.
func (m Money) MulDiv(num, den int64, mode Rounding) (Money, error) {
	if den == 0 {
		return 0, fmt.Errorf("%w: %s × %d ÷ 0", ErrDivideByZero, m, num)
	}
	if !mode.valid() {
		return 0, fmt.Errorf("%w: %s × %d ÷ %d", ErrNoRounding, m, num, den)
	}

	product := new(big.Int).Mul(big.NewInt(int64(m)), big.NewInt(num))
	divisor := big.NewInt(den)
	quotient, remainder := new(big.Int).QuoRem(product, divisor, new(big.Int))

	if remainder.Sign() != 0 {
		// QuoRem truncates toward zero, so every mode below is expressed as
		// "leave it, or step one centavo further away from zero".
		away := big.NewInt(1)
		if product.Sign() != divisor.Sign() {
			away.Neg(away)
		}
		twiceRemainder := new(big.Int).Abs(remainder)
		twiceRemainder.Lsh(twiceRemainder, 1)
		half := twiceRemainder.Cmp(new(big.Int).Abs(divisor))

		switch mode {
		case RoundUp:
			quotient.Add(quotient, away)
		case RoundHalfUp:
			if half >= 0 {
				quotient.Add(quotient, away)
			}
		case RoundHalfEven:
			if half > 0 || (half == 0 && quotient.Bit(0) == 1) {
				quotient.Add(quotient, away)
			}
		case RoundDown:
			// The truncation QuoRem already did.
		}
	}

	if !quotient.IsInt64() {
		return 0, fmt.Errorf("%w: %s × %d ÷ %d", ErrOverflow, m, num, den)
	}
	return Money(quotient.Int64()), nil
}

// String renders the amount the way it goes on the wire and into a log:
// "1234.50", "-99.05", "0.00". No thousands separators and no peso sign — those
// are presentation, and presentation is the frontend's job.
func (m Money) String() string {
	magnitude := uint64(m)
	if m < 0 {
		// Written this way rather than as uint64(-m) because the most negative
		// int64 cannot be negated.
		magnitude = uint64(-(int64(m) + 1)) + 1
	}
	text := fmt.Sprintf("%d.%02d", magnitude/centavosPerPeso, magnitude%centavosPerPeso)
	if m < 0 {
		return "-" + text
	}
	return text
}

// grouped matches either a plainly written integer part or one grouped in
// thousands. "1,234" and "1234" are both an amount; "1,23" and "12,34,567" are
// somebody's decimal comma or somebody's typo, and guessing which would be the
// worst possible service to a lending ledger.
var grouped = regexp.MustCompile(`^(?:\d+|\d{1,3}(?:,\d{3})+)$`)

// Parse reads an amount a person typed.
//
// It is tolerant of what a counter clerk actually types — a peso sign, spaces,
// thousands separators — and intolerant of anything that could mean two things.
// Silently accepting an ambiguous amount is how a ₱1.23 entry becomes a ₱123
// entry, which is not a class of bug this system may have.
func Parse(text string) (Money, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', ' ', ' ', '₱':
			return -1
		}
		return r
	}, text)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return 0, fmt.Errorf("%w: no amount was given", ErrNotAnAmount)
	}

	negative := false
	switch cleaned[0] {
	case '-':
		negative, cleaned = true, cleaned[1:]
	case '+':
		cleaned = cleaned[1:]
	}

	whole, fraction, hasFraction := strings.Cut(cleaned, ".")
	if strings.Contains(fraction, ".") {
		return 0, fmt.Errorf("%w: %q has more than one decimal point", ErrNotAnAmount, text)
	}
	if hasFraction && len(fraction) > 2 {
		return 0, fmt.Errorf(
			"%w: %q has %d decimal places, and the smallest unit of money here is the centavo",
			ErrNotAnAmount, text, len(fraction))
	}
	if hasFraction && fraction == "" {
		return 0, fmt.Errorf("%w: %q ends in a decimal point", ErrNotAnAmount, text)
	}
	if !grouped.MatchString(whole) {
		if strings.Contains(whole, ",") {
			return 0, fmt.Errorf(
				"%w: the commas in %q are not thousands separators — write it as 1,234.50 or 1234.50",
				ErrNotAnAmount, text)
		}
		return 0, fmt.Errorf("%w: %q", ErrNotAnAmount, text)
	}

	// Centavos are the digits of the whole part followed by the two digits of
	// the fraction, which is why this parses as one integer rather than as a
	// peso part and a centavo part that then have to be combined.
	digits := strings.ReplaceAll(whole, ",", "") + (fraction + "00")[:2]
	if negative {
		digits = "-" + digits
	}
	centavos, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return 0, fmt.Errorf("%w: %q", ErrOverflow, text)
		}
		return 0, fmt.Errorf("%w: %q", ErrNotAnAmount, text)
	}
	return Money(centavos), nil
}

// MarshalJSON writes the amount as a string. See the package comment: this is
// the whole reason the type exists.
func (m Money) MarshalJSON() ([]byte, error) {
	return []byte(`"` + m.String() + `"`), nil
}

// UnmarshalJSON reads a string amount, and refuses a JSON number outright.
//
// Refusing rather than accepting matters: a number arriving here means some
// caller is holding a peso amount in a JavaScript float, and the moment to say
// so is now, not after the rounding error reaches a balance.
func (m *Money) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed[0] != '"' {
		return fmt.Errorf(
			"%w: %s was sent as a bare JSON value; a peso amount must be a string like \"1234.50\", "+
				"because JavaScript cannot hold one exactly", ErrNotAnAmount, trimmed)
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("%w: %s", ErrNotAnAmount, trimmed)
	}
	parsed, err := Parse(text)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// Value stores the amount as a SQLite INTEGER of centavos.
func (m Money) Value() (driver.Value, error) { return int64(m), nil }

// Scan reads one back. Only an integer is accepted: a REAL column holding a
// peso amount is the legacy system's mistake, and it should fail here loudly
// rather than be converted into something that looks fine.
func (m *Money) Scan(src any) error {
	switch v := src.(type) {
	case int64:
		*m = Money(v)
		return nil
	case nil:
		*m = Zero
		return nil
	default:
		return fmt.Errorf("money: cannot read a peso amount from %T — the column should be INTEGER centavos", src)
	}
}
