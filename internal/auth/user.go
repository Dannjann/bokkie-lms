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

// The account transitions, all Tx-only.
//
// Every one of these is audited, and the audit entry has to commit with the
// change rather than after it — so there is deliberately no non-Tx variant to
// reach for. The route wrapper in internal/api hands the transaction down.

var (
	// ErrLastManager is the refusal that keeps the station able to administer
	// itself. Undoing a demotion or a deactivation needs a manager, and if the
	// last one is the account being changed there is nobody left to do it —
	// the fix is a sqlite3 shell on the host, held by whoever least wants it.
	ErrLastManager = errors.New("this is the last active manager")

	// ErrSelfChange covers deactivating or demoting yourself. Distinct from
	// ErrLastManager because the sentence a person needs to read is different:
	// one says "somebody else has to do this", the other "you cannot do this
	// to yourself".
	ErrSelfChange = errors.New("an account cannot do this to itself")
)

// SetActiveTx turns an account on or off and returns it as it now stands.
//
// Deactivation takes effect on the deactivated user's *next request*, with no
// session sweeping here: withSession in internal/api re-reads the account on
// every request and ends the session when it finds it inactive. That is a
// deliberate design in Step 2 and this function depends on it.
func SetActiveTx(ctx context.Context, tx *sql.Tx, id int64, active bool) (User, error) {
	return updateUserTx(ctx, tx, id, `UPDATE users SET active = ? WHERE id = ?`, boolToInt(active), id)
}

// SetRoleTx changes an account's role and returns it as it now stands. Like
// deactivation, it takes effect on the next request, because Role is re-read
// from the database on that same path.
func SetRoleTx(ctx context.Context, tx *sql.Tx, id int64, role Role) (User, error) {
	if !role.Valid() {
		return User{}, ValidationError{Field: "role", Message: fmt.Sprintf("%q is not a role", role)}
	}
	return updateUserTx(ctx, tx, id, `UPDATE users SET role = ? WHERE id = ?`, string(role), id)
}

// SetPasswordTx replaces an account's password.
//
// This is the one transition that does *not* ride on withSession re-reading the
// account: the hash changes and the session rows do not, so a caller must also
// delete the sessions it means to end. DeleteSessionsForUserTx is next door,
// and the API routes call both.
func SetPasswordTx(ctx context.Context, tx *sql.Tx, id int64, password string) (User, error) {
	if err := ValidatePassword(password); err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	return updateUserTx(ctx, tx, id, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
}

// VerifyPasswordTx reports whether password is the account's current one. It
// exists for the self-service change, which requires the current password so
// that a hijacked session cannot lock the owner out of their own account.
//
// A missing account and a wrong password are separate errors here and are
// deliberately not separated by the route that calls this.
func VerifyPasswordTx(ctx context.Context, tx *sql.Tx, id int64, password string) error {
	var hash string
	err := tx.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSuchUser
	}
	if err != nil {
		return fmt.Errorf("look up user %d: %w", id, err)
	}
	ok, err := VerifyPassword(hash, password)
	if err != nil {
		return err
	}
	if !ok {
		return ErrWrongPassword
	}
	return nil
}

// CountActiveManagersTx counts the accounts that can still administer this
// system.
//
// **Call this after the UPDATE, inside the same transaction, never before it.**
// A count taken first is a read-then-write, and two managers demoting each
// other at the same moment would each read "two" and each succeed, leaving
// zero. Writing first is what makes the second transaction fail as SQLITE_BUSY
// instead — the ordering Step 5 established and Step 7 restated.
func CountActiveManagersTx(ctx context.Context, tx *sql.Tx) (int, error) {
	var count int
	err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE active = 1 AND role = ?`, string(RoleManager)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count active managers: %w", err)
	}
	return count, nil
}

// updateUserTx runs one single-column UPDATE and reads the account back, so
// every transition returns the same fully-populated User and no caller has to
// assemble one from what it happened to pass in.
func updateUserTx(ctx context.Context, tx *sql.Tx, id int64, query string, args ...any) (User, error) {
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return User{}, fmt.Errorf("update user %d: %w", id, err)
	}
	// A row count of zero is the only way to tell "no such account" from
	// "changed it to what it already was", and the caller needs that
	// distinction to answer 404 rather than 200.
	n, err := res.RowsAffected()
	if err != nil {
		return User{}, fmt.Errorf("update user %d: %w", id, err)
	}
	if n == 0 {
		return User{}, ErrNoSuchUser
	}
	return UserByIDTx(ctx, tx, id)
}

// UserByIDTx is ByID against a transaction, so a transition reads back what it
// just wrote rather than what another connection can see — and so a handler can
// capture the "before" state inside the transaction that is about to change it.
func UserByIDTx(ctx context.Context, tx *sql.Tx, id int64) (User, error) {
	var (
		user      User
		createdAt string
		lastLogin sql.NullString
		active    int
	)
	err := tx.QueryRowContext(ctx,
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

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
