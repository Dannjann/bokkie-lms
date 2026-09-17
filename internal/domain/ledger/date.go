package ledger

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// Date is a day on the station's calendar: no time of day, no timezone.
//
// A ledger entry's date is the day the money moved for the people standing at
// the counter. That is not the same thing as an instant, and storing it as one
// is how a payment taken at half past eight on a Tuesday evening ends up filed
// on Wednesday — Manila is eight hours ahead of UTC, and a statement cut on the
// wrong side of midnight is the kind of error a farmer notices and nobody can
// explain.
//
// It stores and compares as "2026-01-10", which sorts chronologically as text.
// That is what lets SQLite do date ranges with a plain string comparison and an
// ordinary index.
type Date struct {
	year  int
	month time.Month
	day   int
}

const dateLayout = "2006-01-02"

// NewDate builds a date. It does not normalise: 31 February is refused rather
// than quietly becoming 3 March, because a date that wrong came from a bug and
// should surface as one.
func NewDate(year int, month time.Month, day int) (Date, error) {
	t := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if t.Year() != year || t.Month() != month || t.Day() != day {
		return Date{}, fmt.Errorf("%04d-%02d-%02d is not a real date", year, month, day)
	}
	return Date{year: year, month: month, day: day}, nil
}

// DateOf is the calendar day t falls on, in t's own location. Callers pass a
// time already in the station's timezone; see Service.today.
func DateOf(t time.Time) Date {
	return Date{year: t.Year(), month: t.Month(), day: t.Day()}
}

// ParseDate reads the stored form.
func ParseDate(text string) (Date, error) {
	t, err := time.Parse(dateLayout, text)
	if err != nil {
		return Date{}, fmt.Errorf("%q is not a date in YYYY-MM-DD form", text)
	}
	return DateOf(t), nil
}

// MustDate is for tests and for dates that are compiled in. It panics on a
// date that is not real, which in those two settings is a programming error
// found the moment it is introduced.
func MustDate(year int, month time.Month, day int) Date {
	d, err := NewDate(year, month, day)
	if err != nil {
		panic(err)
	}
	return d
}

func (d Date) IsZero() bool { return d == Date{} }

func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.year, int(d.month), d.day)
}

// Time is the date at midnight in loc, for the rare caller that genuinely needs
// an instant — a report header, a filename.
func (d Date) Time(loc *time.Location) time.Time {
	return time.Date(d.year, d.month, d.day, 0, 0, 0, 0, loc)
}

// Before, After and Equal compare calendar days. The zero Date sorts before
// every real one, which is what makes an unset "from" mean "since the
// beginning" without a special case.
func (d Date) Before(other Date) bool { return d.String() < other.String() }
func (d Date) After(other Date) bool  { return d.String() > other.String() }
func (d Date) Equal(other Date) bool  { return d == other }

// AddDays returns the date n days later, or earlier when n is negative.
func (d Date) AddDays(n int) Date {
	return DateOf(d.Time(time.UTC).AddDate(0, 0, n))
}

// Year, Month and Day open the parts up for the callers that build a date from
// one of them — the first of this year, the end of last month — without letting
// anybody assemble a Date that ParseDate would have refused.
func (d Date) Year() int         { return d.year }
func (d Date) Month() time.Month { return d.month }
func (d Date) Day() int          { return d.day }

// DaysSince is how many whole calendar days lie between earlier and d.
//
// Computed in UTC on both sides, which is not a timezone decision but the
// absence of one: both dates are already calendar days with no instant behind
// them, and using the same zone for each makes the subtraction exact. Doing it
// in a zone with daylight saving would make some days 23 hours long and round a
// 30-day-old debt into the wrong bucket twice a year.
//
// Negative when earlier is after d, which the callers clamp; it is not this
// type's business to decide what a date in the future means.
func (d Date) DaysSince(earlier Date) int {
	hours := d.Time(time.UTC).Sub(earlier.Time(time.UTC)).Hours()
	return int(hours) / 24
}

// MarshalJSON and UnmarshalJSON use the same text as the database, so a date
// looks identical in a row, in a log line and on the wire.
func (d Date) MarshalJSON() ([]byte, error) {
	return []byte(`"` + d.String() + `"`), nil
}

func (d *Date) UnmarshalJSON(data []byte) error {
	text := string(data)
	if len(text) < 2 || text[0] != '"' {
		return fmt.Errorf("a date must be a JSON string like \"2026-01-10\", got %s", text)
	}
	text = text[1 : len(text)-1]
	if text == "" {
		*d = Date{}
		return nil
	}
	parsed, err := ParseDate(text)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

func (d Date) Value() (driver.Value, error) { return d.String(), nil }

func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case string:
		if v == "" {
			*d = Date{}
			return nil
		}
		parsed, err := ParseDate(v)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case nil:
		*d = Date{}
		return nil
	default:
		return fmt.Errorf("cannot read a date from %T; the column should be TEXT in YYYY-MM-DD form", src)
	}
}
