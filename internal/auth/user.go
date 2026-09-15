package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Role is what a user is allowed to do. Two, and only two, until a feature
// genuinely needs a third — roles multiply easily and are hard to remove once
// anything checks for them.
type Role string

const (
	// RoleEncoder is the counter staff: create customers, post advances and
	// payments, print slips. The everyday role.
	RoleEncoder Role = "encoder"

	// RoleManager additionally holds the destructive powers. Voiding a posted
	// voucher — the single most sensitive action in the system, arriving in
	// step 5 — gates on this.
	RoleManager Role = "manager"
)

// Valid reports whether r is a role this system knows.
func (r Role) Valid() bool { return r == RoleEncoder || r == RoleManager }

// User is an account. The password hash is deliberately not a field: nothing
// outside this package has a reason to hold one, and a struct that carries it
// eventually gets marshalled into a response by accident.
type User struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	Role        Role       `json:"role"`
	Active      bool       `json:"active"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

var (
	// ErrNoSuchUser and ErrWrongPassword are separate internally so the audit
	// entry can say which it was. They are never separated in a response: the
	// login endpoint answers "username or password is incorrect" either way,
	// because telling an unauthenticated caller which half was right hands
	// them a list of valid usernames.
	ErrNoSuchUser    = errors.New("no such user")
	ErrWrongPassword = errors.New("wrong password")
	ErrUserInactive  = errors.New("account is deactivated")

	// ErrAlreadySetUp guards the first-run flow. Once an admin exists, the
	// create-admin page must never be reachable again.
	ErrAlreadySetUp = errors.New("setup has already been completed")

	ErrUsernameTaken = errors.New("that username is already in use")
)

// Users is the account store.
type Users struct{ db *sql.DB }

// NewUsers returns a store over database.
func NewUsers(database *sql.DB) *Users { return &Users{db: database} }

// NeedsSetup reports whether there are no accounts at all, which is what puts
// the app into first-run mode. It counts rather than checking existence of an
// admin specifically: any account means someone has been here.
func (u *Users) NeedsSetup(ctx context.Context) (bool, error) {
	var count int
	if err := u.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return count == 0, nil
}

// CreateFirstAdmin creates the initial manager account, and refuses if any
// account already exists.
//
// The check and the insert are one transaction on purpose. Two people opening
// the setup page on two counter PCs at the same moment is exactly the
// circumstance a fresh install produces, and the loser of that race must get a
// clean refusal rather than a second admin.
func (u *Users) CreateFirstAdmin(ctx context.Context, username, displayName, password string) (User, error) {
	tx, err := u.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("create first admin: %w", err)
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return User{}, fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return User{}, ErrAlreadySetUp
	}

	user, err := insertUser(ctx, tx, username, displayName, password, RoleManager)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("create first admin: %w", err)
	}
	return user, nil
}

// Create adds an account. Used by the manager-only user administration route.
func (u *Users) Create(ctx context.Context, username, displayName, password string, role Role) (User, error) {
	return insertUser(ctx, u.db, username, displayName, password, role)
}

// CreateUserTx adds an account inside an existing transaction, so the new user
// and the audit entry recording their creation commit together.
func CreateUserTx(ctx context.Context, tx *sql.Tx, username, displayName, password string, role Role) (User, error) {
	return insertUser(ctx, tx, username, displayName, password, role)
}

// All lists every account, newest last. There are never more than a handful of
// these, so there is nothing to paginate.
func (u *Users) All(ctx context.Context) ([]User, error) {
	rows, err := u.db.QueryContext(ctx,
		`SELECT id, username, display_name, role, active, created_at, last_login_at
		   FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	users := []User{}
	for rows.Next() {
		var (
			user      User
			createdAt string
			lastLogin sql.NullString
			active    int
		)
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName,
			&user.Role, &active, &createdAt, &lastLogin); err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}
		user.Active = active == 1
		user.CreatedAt = parseTime(createdAt)
		if lastLogin.Valid {
			t := parseTime(lastLogin.String)
			user.LastLoginAt = &t
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

type queryExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func insertUser(ctx context.Context, db queryExecer, username, displayName, password string, role Role) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	displayName = strings.TrimSpace(displayName)

	if err := ValidateUsername(username); err != nil {
		return User{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return User{}, err
	}
	if !role.Valid() {
		return User{}, ValidationError{Field: "role", Message: fmt.Sprintf("%q is not a role", role)}
	}
	if displayName == "" {
		// The slip and the audit screen show a person, not a login. Defaulting
		// beats rejecting: whoever is standing at a fresh install wants to get
		// in, not to argue about a field.
		displayName = username
	}

	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}

	now := time.Now().UTC()
	res, err := db.ExecContext(ctx,
		`INSERT INTO users (username, display_name, password_hash, role, active, created_at)
		 VALUES (?, ?, ?, ?, 1, ?)`,
		username, displayName, hash, string(role), now.Format(time.RFC3339Nano))
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrUsernameTaken
		}
		return User{}, fmt.Errorf("create user %s: %w", username, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("create user %s: %w", username, err)
	}
	return User{
		ID:          id,
		Username:    username,
		DisplayName: displayName,
		Role:        role,
		Active:      true,
		CreatedAt:   now,
	}, nil
}

// Authenticate checks a username and password.
//
// A deactivated account is rejected *after* the password check so that
// deactivation is not detectable by response timing — and so the audit entry
// can distinguish "wrong password" from "correct password, account disabled",
// which is a materially different event to read months later.
func (u *Users) Authenticate(ctx context.Context, username, password string) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))

	var (
		user      User
		hash      string
		createdAt string
		lastLogin sql.NullString
		active    int
	)
	err := u.db.QueryRowContext(ctx,
		`SELECT id, username, display_name, password_hash, role, active, created_at, last_login_at
		   FROM users WHERE username = ?`, username,
	).Scan(&user.ID, &user.Username, &user.DisplayName, &hash, &user.Role, &active, &createdAt, &lastLogin)

	if errors.Is(err, sql.ErrNoRows) {
		// Hash anyway. Returning immediately makes "no such user" measurably
		// faster than "wrong password", which turns the login form into a
		// username oracle.
		_, _ = VerifyPassword(dummyHash, password)
		return User{}, ErrNoSuchUser
	}
	if err != nil {
		return User{}, fmt.Errorf("look up user %s: %w", username, err)
	}

	ok, err := VerifyPassword(hash, password)
	if err != nil {
		return User{}, fmt.Errorf("verify password for %s: %w", username, err)
	}
	if !ok {
		return User{}, ErrWrongPassword
	}
	if active != 1 {
		return User{}, ErrUserInactive
	}

	user.Active = true
	user.CreatedAt = parseTime(createdAt)
	if lastLogin.Valid {
		t := parseTime(lastLogin.String)
		user.LastLoginAt = &t
	}
	return user, nil
}

// dummyHash is a real argon2id hash of a value nobody knows, used to spend the
// same time on an unknown username as on a known one.
var dummyHash = func() string {
	h, err := HashPassword("bokkie-timing-equaliser")
	if err != nil {
		// Only reachable if crypto/rand fails, in which case nothing about
		// this process is trustworthy.
		panic("auth: cannot hash: " + err.Error())
	}
	return h
}()

// MarkLoggedIn records a successful login. A failure here is not worth failing
// the login over — the session is already valid — so the caller logs it.
func (u *Users) MarkLoggedIn(ctx context.Context, id int64, at time.Time) error {
	_, err := u.db.ExecContext(ctx,
		`UPDATE users SET last_login_at = ? WHERE id = ?`,
		at.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("record login for user %d: %w", id, err)
	}
	return nil
}

// ByID loads an account, used when resolving a session cookie back to a user.
func (u *Users) ByID(ctx context.Context, id int64) (User, error) {
	var (
		user      User
		createdAt string
		lastLogin sql.NullString
		active    int
	)
	err := u.db.QueryRowContext(ctx,
		`SELECT id, username, display_name, role, active, created_at, last_login_at
		   FROM users WHERE id = ?`, id,
	).Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &active, &createdAt, &lastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoSuchUser
	}
	if err != nil {
		return User{}, fmt.Errorf("look up user %d: %w", id, err)
	}

	user.Active = active == 1
	user.CreatedAt = parseTime(createdAt)
	if lastLogin.Valid {
		t := parseTime(lastLogin.String)
		user.LastLoginAt = &t
	}
	return user, nil
}

// isUniqueViolation recognises a duplicate username without depending on the
// driver's error type. modernc.org/sqlite wraps its codes, and matching on the
// message is stable enough for the one constraint that has a user-facing
// meaning.
func isUniqueViolation(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") || strings.Contains(msg, "constraint failed: users.username")
}

// parseTime reads a stored RFC3339 timestamp. Everything written to these
// columns went through Format, so a parse failure means hand-editing; a zero
// time reads as obviously wrong on screen, which is the right outcome.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
