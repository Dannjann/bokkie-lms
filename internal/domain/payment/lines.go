package payment

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// A palay delivery is recorded the way the legacy "Payments From Customer"
// screen recorded it: a line per lot, each with its class, sacks, kilos and
// price per kilo, and the cost worked out here as kilos × price. The legacy
// PAYTRAN table holds a thousand of these; it is how the station has been paid
// for eighteen years.

// Kilos is a weight to one decimal place, held as tenths of a kilo: 1234.5 kg
// is 12345. Tenths because the legacy column was N(10,1), and an integer
// because a weight multiplied by a price is a peso amount, and peso amounts do
// not go near a float.
type Kilos int64

// KilosFromTenths is the constructor tests and the database use.
func KilosFromTenths(t int64) Kilos { return Kilos(t) }

// Tenths is the stored form.
func (k Kilos) Tenths() int64 { return int64(k) }

// String prints a weight the way it is written on a weighing slip: 1234.5, or
// 1234.0 for a round one, so a column of them lines up.
func (k Kilos) String() string {
	sign := ""
	t := int64(k)
	if t < 0 {
		sign, t = "-", -t
	}
	return fmt.Sprintf("%s%d.%d", sign, t/10, t%10)
}

// ParseKilos reads "1234", "1234.5" or "1,234.5". More than one decimal place
// is refused rather than rounded: the scale does not weigh to the gram, so a
// second decimal is a typing mistake, and rounding it away would hide one.
func ParseKilos(text string) (Kilos, error) {
	s := strings.ReplaceAll(strings.TrimSpace(text), ",", "")
	if s == "" {
		return 0, fmt.Errorf("a weight is needed")
	}
	whole, frac, hasPoint := strings.Cut(s, ".")
	if hasPoint && len(frac) != 1 {
		if len(frac) == 0 {
			frac = "0"
		} else {
			return 0, fmt.Errorf("%q has more than one decimal place — weigh to the tenth of a kilo", text)
		}
	}
	if !hasPoint {
		frac = "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w < 0 || strings.HasPrefix(whole, "+") || strings.HasPrefix(whole, "-") {
		return 0, fmt.Errorf("%q is not a weight", text)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("%q is not a weight", text)
	}
	if w > (1<<62)/10 {
		return 0, fmt.Errorf("%q is too heavy to be a delivery", text)
	}
	return Kilos(w*10 + f), nil
}

// MarshalJSON writes a weight as a string, as money does, so the browser never
// holds it as a float either.
func (k Kilos) MarshalJSON() ([]byte, error) { return []byte(strconv.Quote(k.String())), nil }

// UnmarshalJSON accepts a string — "1234.5" — and nothing else.
func (k *Kilos) UnmarshalJSON(data []byte) error {
	text, err := strconv.Unquote(string(data))
	if err != nil {
		return fmt.Errorf("a weight must be sent as a string, as \"1234.5\"")
	}
	parsed, err := ParseKilos(text)
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

// LineDraft is one lot of palay as typed at the counter.
type LineDraft struct {
	Class string      `json:"class"`
	Sacks int64       `json:"sacks"`
	Kilos Kilos       `json:"kilos"`
	Price money.Money `json:"price"` // per kilo
}

// Line is one lot of palay on a payment, with its cost.
type Line struct {
	ID    int64       `json:"id"`
	Class string      `json:"class"`
	Sacks int64       `json:"sacks"`
	Kilos Kilos       `json:"kilos"`
	Price money.Money `json:"price"`
	Cost  money.Money `json:"cost"`
}

// maxLines is generous for one delivery: the most the legacy table ever held
// for a single farmer's open deliveries is a handful.
const maxLines = 50

const maxClassLen = 30

// Cost is kilos × price per kilo, rounded half-up to the centavo — what a
// person does with a calculator, and what the legacy screen did.
func Cost(kilos Kilos, price money.Money) (money.Money, error) {
	return price.MulDiv(kilos.Tenths(), 10, money.RoundHalfUp)
}

// cleanLines checks the lines of a palay payment and works out each cost and
// the total.
func cleanLines(in []LineDraft) ([]Line, money.Money, error) {
	if len(in) == 0 {
		return nil, money.Zero, ValidationError{Field: "lines", Message: "a palay payment needs at least one line"}
	}
	if len(in) > maxLines {
		return nil, money.Zero, ValidationError{
			Field: "lines", Message: fmt.Sprintf("a payment takes %d lines at most", maxLines)}
	}

	out := make([]Line, 0, len(in))
	total := money.Zero
	for i, d := range in {
		row := i + 1
		line := Line{Class: strings.ToUpper(strings.TrimSpace(d.Class)), Sacks: d.Sacks, Kilos: d.Kilos, Price: d.Price}
		switch {
		case len([]rune(line.Class)) > maxClassLen:
			return nil, money.Zero, ValidationError{
				Field: "lines", Message: fmt.Sprintf("line %d: the class is too long — %d characters at most", row, maxClassLen)}
		case line.Sacks < 0:
			return nil, money.Zero, ValidationError{
				Field: "lines", Message: fmt.Sprintf("line %d: sacks cannot be negative", row)}
		case line.Kilos.Tenths() <= 0:
			return nil, money.Zero, ValidationError{
				Field: "lines", Message: fmt.Sprintf("line %d needs a weight in kilos", row)}
		case !line.Price.IsPositive():
			return nil, money.Zero, ValidationError{
				Field: "lines", Message: fmt.Sprintf("line %d needs a price per kilo", row)}
		}
		cost, err := Cost(line.Kilos, line.Price)
		if err != nil {
			return nil, money.Zero, ValidationError{
				Field: "lines", Message: fmt.Sprintf("line %d: %s kg at %s is too large to add up", row, line.Kilos, line.Price)}
		}
		if !cost.IsPositive() {
			return nil, money.Zero, ValidationError{
				Field: "lines", Message: fmt.Sprintf("line %d comes to nothing — check the weight and the price", row)}
		}
		line.Cost = cost
		if total, err = total.Add(cost); err != nil {
			return nil, money.Zero, ValidationError{Field: "lines", Message: "those lines are too large to add up"}
		}
		out = append(out, line)
	}
	return out, total, nil
}

// writeLines replaces a draft's lines. Only ever called on a draft — the
// schema refuses it otherwise.
func writeLines(ctx context.Context, q queryExecer, paymentID int64, lines []Line) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM payment_lines WHERE payment_id = ?`, paymentID); err != nil {
		return fmt.Errorf("clear the lines of payment %d: %w", paymentID, err)
	}
	for _, l := range lines {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO payment_lines (payment_id, class, sacks, kilos_tenths, price_cents, cost_cents)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			paymentID, l.Class, l.Sacks, l.Kilos.Tenths(), l.Price, l.Cost); err != nil {
			return fmt.Errorf("write a line of payment %d: %w", paymentID, err)
		}
	}
	return nil
}

// linesOf reads a payment's lines, in the order they were typed.
func linesOf(ctx context.Context, q queryExecer, paymentID int64) ([]Line, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, class, sacks, kilos_tenths, price_cents, cost_cents
		   FROM payment_lines WHERE payment_id = ? ORDER BY id`, paymentID)
	if err != nil {
		return nil, fmt.Errorf("read the lines of payment %d: %w", paymentID, err)
	}
	defer rows.Close()

	lines := []Line{}
	for rows.Next() {
		var l Line
		var tenths int64
		if err := rows.Scan(&l.ID, &l.Class, &l.Sacks, &tenths, &l.Price, &l.Cost); err != nil {
			return nil, fmt.Errorf("read the lines of payment %d: %w", paymentID, err)
		}
		l.Kilos = KilosFromTenths(tenths)
		lines = append(lines, l)
	}
	return lines, rows.Err()
}
