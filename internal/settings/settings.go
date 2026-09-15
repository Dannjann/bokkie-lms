package settings

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Store reads and writes settings, caching them in memory.
//
// The cache is populated once at open and updated on every write, so a changed
// setting takes effect immediately without a restart — that is the whole point
// of the table. Reads are lock-free-ish (RWMutex) because the backup scheduler
// and every request that renders a date will hit them.
type Store struct {
	db *sql.DB

	mu     sync.RWMutex
	values map[string]string
	loc    *time.Location // parsed form of Timezone, kept hot
}

// Open loads the store, seeding any setting a previous version did not know
// about. Seeding on every start rather than only on first run means adding a
// Def to defs.go is the only step needed to introduce a setting — an upgrade
// fills it in on the next boot.
func Open(ctx context.Context, db *sql.DB) (*Store, error) {
	s := &Store{db: db, values: make(map[string]string, len(All))}
	if err := s.seed(ctx); err != nil {
		return nil, err
	}
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) seed(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("seed settings: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, d := range All {
		// DO NOTHING, not REPLACE: a setting someone has already changed must
		// survive an upgrade. Only genuinely absent keys get a default.
		_, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			 ON CONFLICT (key) DO NOTHING`,
			d.Key, d.Default, now)
		if err != nil {
			return fmt.Errorf("seed setting %s: %w", d.Key, err)
		}
	}
	return tx.Commit()
}

// Reload re-reads every setting from the database into the cache.
//
// It is exported because a setting changed inside someone else's transaction is
// not visible here until that transaction commits — so the API calls this after
// the commit, which is what makes a changed setting take effect immediately
// rather than on the next restart.
func (s *Store) Reload(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}
	defer rows.Close()

	values := make(map[string]string, len(All))
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return fmt.Errorf("read settings: %w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read settings: %w", err)
	}

	loc, err := time.LoadLocation(values[Timezone.Key])
	if err != nil {
		return fmt.Errorf("setting %s is %q, which is not a known timezone: %w",
			Timezone.Key, values[Timezone.Key], err)
	}

	s.mu.Lock()
	s.values, s.loc = values, loc
	s.mu.Unlock()
	return nil
}

// Raw is the stored text of a setting.
func (s *Store) Raw(d Def) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.values[d.Key]; ok {
		return v
	}
	return d.Default
}

// String, Int, Bool and TimeOfDay read a setting in its declared type.
//
// They fall back to the default rather than returning an error. Every stored
// value was validated by Set before it was written, so a parse failure here
// means the row was edited by hand in a sqlite3 shell — and the station
// continuing to run on the default beats the backup scheduler refusing to
// start because someone typo'd a time.
func (s *Store) String(d Def) string { return s.Raw(d) }

func (s *Store) Int(d Def) int {
	n, err := strconv.Atoi(strings.TrimSpace(s.Raw(d)))
	if err != nil {
		n, _ = strconv.Atoi(d.Default)
	}
	return n
}

func (s *Store) Bool(d Def) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(s.Raw(d)))
	if err != nil {
		b, _ = strconv.ParseBool(d.Default)
	}
	return b
}

// TimeOfDay returns the hour and minute of an HH:MM setting.
func (s *Store) TimeOfDay(d Def) (hour, minute int) {
	h, m, err := parseTimeOfDay(s.Raw(d))
	if err != nil {
		h, m, _ = parseTimeOfDay(d.Default)
	}
	return h, m
}

// Location is the station's timezone, parsed. Business dates are rendered
// through this; storage stays UTC.
func (s *Store) Location() *time.Location {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loc
}

// Set validates and stores a value, then updates the cache so the next read
// sees it. The validation is what lets every reader above fall back to a
// default instead of handling errors.
func (s *Store) Set(ctx context.Context, d Def, value string) error {
	value = strings.TrimSpace(value)
	if err := Validate(d, value); err != nil {
		return err
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		d.Key, value, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("write setting %s: %w", d.Key, err)
	}
	return s.Reload(ctx)
}

// Validate checks a value against its declared kind. It is exported because the
// API validates a request before deciding what status code to return.
func Validate(d Def, value string) error {
	switch d.Kind {
	case KindInt:
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s must be a whole number, got %q", d.Key, value)
		}
		// Every numeric setting we have is a count or a starting number, and
		// none of them mean anything at zero or below.
		if n < 1 {
			return fmt.Errorf("%s must be at least 1, got %d", d.Key, n)
		}
	case KindBool:
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("%s must be true or false, got %q", d.Key, value)
		}
	case KindTimeOfDay:
		if _, _, err := parseTimeOfDay(value); err != nil {
			return fmt.Errorf("%s: %w", d.Key, err)
		}
	case KindTimezone:
		if _, err := time.LoadLocation(value); err != nil {
			return fmt.Errorf("%s is not a known timezone: %q", d.Key, value)
		}
	case KindString:
		if value == "" {
			return fmt.Errorf("%s cannot be empty", d.Key)
		}
	}
	return nil
}

func parseTimeOfDay(value string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, 0, fmt.Errorf("must be a time of day as HH:MM, got %q", value)
	}
	return t.Hour(), t.Minute(), nil
}
