package money

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestParseAcceptsWhatAClerkTypes(t *testing.T) {
	cases := []struct {
		text string
		want int64
	}{
		{"0", 0},
		{"0.00", 0},
		{"1234", 123400},
		{"1234.50", 123450},
		{"1,234.50", 123450},
		{"₱1,234.50", 123450},
		{"₱ 1 234.50", 123450}, // spaces anywhere, including as separators
		{"-99.05", -9905},
		{"+7", 700},
		{"0.5", 50}, // one decimal digit is tenths of a peso, not centavos
		{"0.05", 5},
		{"12,345,678.90", 1234567890},
		{"  1,000.00  ", 100000},
	}

	for _, c := range cases {
		got, err := Parse(c.text)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.text, err)
			continue
		}
		if got.Centavos() != c.want {
			t.Errorf("Parse(%q) = %d centavos, want %d", c.text, got.Centavos(), c.want)
		}
	}
}

// Every case here is one that could plausibly be read two ways. The system's
// answer to an ambiguous amount is to refuse it, because the alternative is a
// ₱1.23 advance silently becoming a ₱123 advance.
func TestParseRefusesAmbiguity(t *testing.T) {
	refused := []string{
		"",
		"   ",
		"abc",
		"1.234",     // three decimals: is that a thousands separator or centavos?
		"1,23",      // a decimal comma, or a mis-typed separator
		"12,34,567", // grouping that is not thousands
		"1.2.3",
		"12.",
		"1 000,50",
		"--5",
		"5-",
		"₱",
	}

	for _, text := range refused {
		if got, err := Parse(text); err == nil {
			t.Errorf("Parse(%q) = %s, want a refusal", text, got)
		}
	}
}

func TestParseRefusesAmountsTooLargeToHold(t *testing.T) {
	_, err := Parse("99999999999999999999.00")
	if !errors.Is(err, ErrOverflow) {
		t.Errorf("parsing a 20-digit amount gave %v, want ErrOverflow", err)
	}
}

func TestStringRendersTwoDecimalsAlways(t *testing.T) {
	cases := map[int64]string{
		0:      "0.00",
		5:      "0.05",
		50:     "0.50",
		123450: "1234.50",
		-9905:  "-99.05",
		-5:     "-0.05",
		1:      "0.01",
		-1:     "-0.01",
	}
	for centavos, want := range cases {
		if got := FromCentavos(centavos).String(); got != want {
			t.Errorf("Money(%d) = %q, want %q", centavos, got, want)
		}
	}

	// The one value with no positive counterpart must still render, not panic.
	if got := FromCentavos(math.MinInt64).String(); !strings.HasPrefix(got, "-") {
		t.Errorf("the most negative amount rendered as %q", got)
	}
}

// The round trip is the contract the frontend depends on. If it ever stops
// holding, a peso amount has been through a float somewhere.
func TestJSONRoundTripsExactly(t *testing.T) {
	amounts := []int64{0, 1, -1, 99, 100, 123450, -9905, math.MaxInt64, math.MinInt64 + 1}

	for _, centavos := range amounts {
		original := FromCentavos(centavos)
		encoded, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("marshal %d: %v", centavos, err)
		}
		if encoded[0] != '"' {
			t.Errorf("%d marshalled as %s, want a JSON string", centavos, encoded)
		}

		var decoded Money
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("unmarshal %s: %v", encoded, err)
		}
		if decoded != original {
			t.Errorf("%s round-tripped to %s", original, decoded)
		}
	}
}

// A bare JSON number means somebody upstream is holding pesos in a float. The
// moment to find out is at the boundary, not after it reaches a balance.
func TestJSONRefusesBareNumbers(t *testing.T) {
	for _, payload := range []string{`1234.50`, `1234`, `null`, `true`} {
		var m Money
		if err := json.Unmarshal([]byte(payload), &m); err == nil {
			t.Errorf("unmarshalling %s succeeded as %s, want a refusal", payload, m)
		}
	}
}

func TestJSONInsideAStruct(t *testing.T) {
	type entry struct {
		Principal Money `json:"principal"`
	}

	encoded, err := json.Marshal(entry{Principal: FromCentavos(123450)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"principal":"1234.50"}`; string(encoded) != want {
		t.Errorf("marshalled to %s, want %s", encoded, want)
	}

	var decoded entry
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Principal.Centavos() != 123450 {
		t.Errorf("decoded %s", decoded.Principal)
	}
}

func TestArithmeticRefusesToWrap(t *testing.T) {
	max := FromCentavos(math.MaxInt64)
	min := FromCentavos(math.MinInt64)

	if _, err := max.Add(FromCentavos(1)); !errors.Is(err, ErrOverflow) {
		t.Errorf("max+1 gave %v, want ErrOverflow", err)
	}
	if _, err := min.Sub(FromCentavos(1)); !errors.Is(err, ErrOverflow) {
		t.Errorf("min-1 gave %v, want ErrOverflow", err)
	}
	if _, err := min.Neg(); !errors.Is(err, ErrOverflow) {
		t.Errorf("-min gave %v, want ErrOverflow", err)
	}
	if _, err := max.MulInt(2); !errors.Is(err, ErrOverflow) {
		t.Errorf("max×2 gave %v, want ErrOverflow", err)
	}
	if _, err := Sum(max, FromCentavos(1), FromCentavos(-1)); !errors.Is(err, ErrOverflow) {
		t.Error("Sum reordered its way around an overflow instead of reporting it")
	}
}

func TestArithmeticIsExact(t *testing.T) {
	a, b := FromCentavos(123450), FromCentavos(9905)

	if sum, _ := a.Add(b); sum.Centavos() != 133355 {
		t.Errorf("sum = %s", sum)
	}
	if diff, _ := a.Sub(b); diff.Centavos() != 113545 {
		t.Errorf("difference = %s", diff)
	}
	if product, _ := b.MulInt(3); product.Centavos() != 29715 {
		t.Errorf("product = %s", product)
	}
	if total, _ := Sum(a, b, FromCentavos(-45)); total.Centavos() != 133310 {
		t.Errorf("total = %s", total)
	}
}

// Division must be told how to round, every time. This is the test that stops
// step 7's interest rule from inheriting whatever Go's / operator happens to do.
func TestDivisionDemandsARoundingMode(t *testing.T) {
	amount := FromCentavos(100)

	if _, err := amount.Div(3, 0); !errors.Is(err, ErrNoRounding) {
		t.Errorf("dividing with the zero rounding mode gave %v, want ErrNoRounding", err)
	}
	if _, err := amount.Div(0, RoundHalfUp); !errors.Is(err, ErrDivideByZero) {
		t.Errorf("dividing by zero gave %v, want ErrDivideByZero", err)
	}
}

func TestRoundingModes(t *testing.T) {
	// 7 ÷ 2 = 3.5 centavos, and −7 ÷ 2 = −3.5: the case every mode disagrees on.
	cases := []struct {
		centavos int64
		divisor  int64
		mode     Rounding
		want     int64
	}{
		{7, 2, RoundHalfUp, 4},
		{7, 2, RoundHalfEven, 4}, // 3.5 → 4, which is the even one
		{5, 2, RoundHalfEven, 2}, // 2.5 → 2, likewise
		{7, 2, RoundDown, 3},
		{7, 2, RoundUp, 4},
		{-7, 2, RoundHalfUp, -4},
		{-7, 2, RoundHalfEven, -4},
		{-7, 2, RoundDown, -3},
		{-7, 2, RoundUp, -4},
		{100, 3, RoundHalfUp, 33},
		{100, 3, RoundUp, 34},
		{99, 3, RoundHalfUp, 33}, // exact: no mode changes an exact quotient
		{99, 3, RoundUp, 33},
		{99, 3, RoundDown, 33},
	}

	for _, c := range cases {
		got, err := FromCentavos(c.centavos).Div(c.divisor, c.mode)
		if err != nil {
			t.Errorf("%d ÷ %d (%s): %v", c.centavos, c.divisor, c.mode, err)
			continue
		}
		if got.Centavos() != c.want {
			t.Errorf("%d ÷ %d (%s) = %d, want %d", c.centavos, c.divisor, c.mode, got.Centavos(), c.want)
		}
	}
}

// A rate applied as num÷den never touches a float and never loses precision to
// an intermediate rounding. Three percent of ₱1,234.50 is ₱37.035, which is
// ₱37.04 rounded the way a person would.
func TestMulDivAppliesARateExactly(t *testing.T) {
	principal := FromCentavos(123450)

	threePercent, err := principal.MulDiv(3, 100, RoundHalfUp)
	if err != nil {
		t.Fatalf("3%%: %v", err)
	}
	if threePercent.Centavos() != 3704 {
		t.Errorf("3%% of %s = %s, want 37.04", principal, threePercent)
	}

	// The intermediate here is far larger than an int64 of centavos could hold.
	// Doing the multiply at full width is what keeps it from overflowing.
	big := FromCentavos(math.MaxInt64 / 2)
	if _, err := big.MulDiv(1000, 1000, RoundHalfUp); err != nil {
		t.Errorf("a rate of exactly 1 on a large amount overflowed: %v", err)
	}
	if _, err := big.MulDiv(1000, 1, RoundHalfUp); !errors.Is(err, ErrOverflow) {
		t.Errorf("a result that genuinely does not fit gave %v, want ErrOverflow", err)
	}
}

func TestScanRefusesAFloatColumn(t *testing.T) {
	var m Money

	if err := m.Scan(int64(123450)); err != nil || m.Centavos() != 123450 {
		t.Errorf("scanning an integer gave %s, %v", m, err)
	}
	if err := m.Scan("1234.50"); err == nil {
		t.Error("scanning text succeeded; a money column must be INTEGER centavos")
	}
	if err := m.Scan(nil); err != nil || !m.IsZero() {
		t.Errorf("scanning NULL gave %s, %v", m, err)
	}
}
