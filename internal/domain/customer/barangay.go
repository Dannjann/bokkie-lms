package customer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Barangay is a place farmers come from, and the prefix their codes carry.
//
// It lives in this package rather than one of its own because its only job is
// to shape customer codes: the allocator in CreateTx reads the prefix in the
// same statement that writes the code, and splitting the two would mean either
// a read-then-write allocator or a package cycle.
type Barangay struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
	Active bool   `json:"active"`

	// Customers is how many farmers point at this barangay. A prefix can only
	// be changed while it is zero, so the screen needs to know.
	Customers int `json:"customers"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BarangayDraft is what a manager types.
type BarangayDraft struct {
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
	Active bool   `json:"active"`
}

var (
	// ErrBarangayNotFound means there is no barangay with that id.
	ErrBarangayNotFound = errors.New("no such barangay")

	// ErrPrefixInUse means somebody tried to change a prefix that codes have
	// already been handed out under.
	ErrPrefixInUse = errors.New(
		"customers already hold codes with this prefix — it cannot change now")
)

var prefixPattern = regexp.MustCompile(`^[A-Z]{2,4}$`)

const maxBarangayNameLen = 80

func (d BarangayDraft) clean() (BarangayDraft, error) {
	out := BarangayDraft{
		Name:   strings.TrimSpace(d.Name),
		Prefix: strings.ToUpper(strings.TrimSpace(d.Prefix)),
		Active: d.Active,
	}
	switch {
	case out.Name == "":
		return BarangayDraft{}, ValidationError{Field: "name", Message: "a barangay needs a name"}
	case len([]rune(out.Name)) > maxBarangayNameLen:
		return BarangayDraft{}, ValidationError{
			Field:   "name",
			Message: fmt.Sprintf("that name is too long — %d characters at most", maxBarangayNameLen),
		}
	case !prefixPattern.MatchString(out.Prefix):
		return BarangayDraft{}, ValidationError{
			Field:   "prefix",
			Message: "a code prefix is two to four letters, like PI or CMG",
		}
	}
	return out, nil
}

// Barangays lists every barangay, active first, then by name.
func (s *Service) Barangays(ctx context.Context) ([]Barangay, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+barangayColumns+` FROM barangays b ORDER BY b.active DESC, b.name`)
	if err != nil {
		return nil, fmt.Errorf("list barangays: %w", err)
	}
	defer rows.Close()

	out := []Barangay{}
	for rows.Next() {
		b, err := scanBarangay(rows)
		if err != nil {
			return nil, fmt.Errorf("list barangays: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list barangays: %w", err)
	}
	return out, nil
}

// BarangayByID loads one barangay.
func (s *Service) BarangayByID(ctx context.Context, id int64) (Barangay, error) {
	return barangayByID(ctx, s.db, id)
}

// CreateBarangayTx adds a barangay. A new one is always active.
func (s *Service) CreateBarangayTx(ctx context.Context, tx *sql.Tx, d BarangayDraft) (Barangay, error) {
	draft, err := d.clean()
	if err != nil {
		return Barangay{}, err
	}
	stamp := s.now().UTC().Format(time.RFC3339Nano)

	res, err := tx.ExecContext(ctx,
		`INSERT INTO barangays (name, prefix, active, created_at, updated_at) VALUES (?, ?, 1, ?, ?)`,
		draft.Name, draft.Prefix, stamp, stamp)
	if err != nil {
		return Barangay{}, barangayWriteError(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Barangay{}, fmt.Errorf("create barangay: %w", err)
	}
	return barangayByID(ctx, tx, id)
}

// UpdateBarangayTx renames a barangay, retires or restores it, and changes its
// prefix — the last only while no customer holds a code under the old one.
//
// The guard is written into the UPDATE itself rather than read first, for the
// same reason the code allocator is: this transaction's first statement is its
// write.
func (s *Service) UpdateBarangayTx(ctx context.Context, tx *sql.Tx, id int64, d BarangayDraft) (Barangay, error) {
	draft, err := d.clean()
	if err != nil {
		return Barangay{}, err
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE barangays SET name = ?, prefix = ?, active = ?, updated_at = ?
		  WHERE id = ?
		    AND (prefix = ? OR NOT EXISTS (
		          SELECT 1 FROM customers c WHERE c.code GLOB barangays.prefix || '-*'))`,
		draft.Name, draft.Prefix, boolToInt(draft.Active),
		s.now().UTC().Format(time.RFC3339Nano), id, draft.Prefix)
	if err != nil {
		return Barangay{}, barangayWriteError(err)
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		// Either it does not exist or its prefix is spoken for; the rollback
		// that follows makes reading now harmless.
		if _, err := barangayByID(ctx, tx, id); err != nil {
			return Barangay{}, err
		}
		return Barangay{}, ErrPrefixInUse
	}
	return barangayByID(ctx, tx, id)
}

const barangayColumns = `b.id, b.name, b.prefix, b.active,
	(SELECT COUNT(*) FROM customers c WHERE c.barangay_id = b.id),
	b.created_at, b.updated_at`

func barangayByID(ctx context.Context, db queryExecer, id int64) (Barangay, error) {
	b, err := scanBarangay(db.QueryRowContext(ctx,
		`SELECT `+barangayColumns+` FROM barangays b WHERE b.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Barangay{}, ErrBarangayNotFound
	}
	if err != nil {
		return Barangay{}, fmt.Errorf("load barangay %d: %w", id, err)
	}
	return b, nil
}

func scanBarangay(row scanner) (Barangay, error) {
	var (
		b                    Barangay
		active               int
		createdAt, updatedAt string
	)
	if err := row.Scan(&b.ID, &b.Name, &b.Prefix, &active, &b.Customers, &createdAt, &updatedAt); err != nil {
		return Barangay{}, err
	}
	b.Active = active == 1
	b.CreatedAt = parseTime(createdAt)
	b.UpdatedAt = parseTime(updatedAt)
	return b, nil
}

// barangayWriteError turns a duplicate into something the manager can fix.
func barangayWriteError(err error) error {
	msg := strings.ToLower(err.Error())
	switch {
	case isUniqueViolation(err) && strings.Contains(msg, "barangays.name"):
		return ValidationError{Field: "name", Message: "there is already a barangay by that name"}
	case isUniqueViolation(err) && strings.Contains(msg, "barangays.prefix"):
		return ValidationError{Field: "prefix", Message: "another barangay already uses that prefix"}
	}
	return fmt.Errorf("write barangay: %w", err)
}
