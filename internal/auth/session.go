package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// CookieName is the session cookie. Prefixed so it is obvious whose it is when
// someone is looking at browser storage on a station PC.
const CookieName = "bokkie_session"

// SessionLifetime is deliberately long.
//
// The realistic usage is a counter PC logged in from opening to closing, every
// day. A short idle timeout does not make that safer; it makes staff share
// passwords and write them on the monitor, because being logged out mid-payment
// is intolerable. So: thirty days, no idle expiry, and an explicit Lock action
// for the case the screen is genuinely left unattended. Physical control of the
// station is the real boundary here, and it is a better one than a timeout.
const SessionLifetime = 30 * 24 * time.Hour

// tokenBytes is the entropy in a session token. 32 bytes is far beyond what a
// LAN with ten users needs, and costs nothing.
const tokenBytes = 32

// ErrNoSession means the request carried no usable session: absent, unknown, or
// expired. Callers do not need to tell those apart — all three mean "log in".
var ErrNoSession = errors.New("no valid session")

// Session is a logged-in browser.
type Session struct {
	ID         string // the SHA-256 of the token, which is what is stored
	UserID     int64
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

// Sessions is the session store. It is backed by SQLite rather than memory so
// sessions survive a service restart — every upgrade is a restart, and logging
// the whole station out to deploy a fix is how a system trains people to resent
// it.
type Sessions struct{ db *sql.DB }

// NewSessions returns a store over database.
func NewSessions(database *sql.DB) *Sessions { return &Sessions{db: database} }

// Create starts a session and returns the token to put in the cookie.
//
// The token is returned to the caller and never stored; only its hash goes in
// the table. A copy of bokkie.db — and this step starts making copies onto a
// USB stick nightly — therefore contains nothing that can be replayed as a
// login.
func (s *Sessions) Create(ctx context.Context, userID int64, ip string) (token string, sess Session, err error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", Session{}, fmt.Errorf("generate session token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)

	now := time.Now().UTC()
	sess = Session{
		ID:         hashToken(token),
		UserID:     userID,
		CreatedAt:  now,
		ExpiresAt:  now.Add(SessionLifetime),
		LastSeenAt: now,
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, created_at, expires_at, last_seen_at, ip)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.UserID,
		sess.CreatedAt.Format(time.RFC3339Nano),
		sess.ExpiresAt.Format(time.RFC3339Nano),
		sess.LastSeenAt.Format(time.RFC3339Nano),
		ip)
	if err != nil {
		return "", Session{}, fmt.Errorf("create session: %w", err)
	}
	return token, sess, nil
}

// Lookup resolves a cookie token to its session, refusing an expired one.
func (s *Sessions) Lookup(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrNoSession
	}

	var (
		sess                             Session
		createdAt, expiresAt, lastSeenAt string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, created_at, expires_at, last_seen_at FROM sessions WHERE id = ?`,
		hashToken(token),
	).Scan(&sess.ID, &sess.UserID, &createdAt, &expiresAt, &lastSeenAt)

	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, fmt.Errorf("look up session: %w", err)
	}

	sess.CreatedAt = parseTime(createdAt)
	sess.ExpiresAt = parseTime(expiresAt)
	sess.LastSeenAt = parseTime(lastSeenAt)

	if !time.Now().UTC().Before(sess.ExpiresAt) {
		// Clean up on the way past. There is no other sweeper, and this is the
		// moment we know for certain the row is dead.
		_ = s.Delete(ctx, sess.ID)
		return Session{}, ErrNoSession
	}
	return sess, nil
}

// Touch records that a session was used. It is called at most once an hour per
// session rather than on every request: last_seen_at exists to answer "is this
// account still in use", not to be accurate to the second, and a write on every
// request would put the whole station behind one row lock all day.
const touchInterval = time.Hour

func (s *Sessions) Touch(ctx context.Context, sess Session) {
	now := time.Now().UTC()
	if now.Sub(sess.LastSeenAt) < touchInterval {
		return
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ? WHERE id = ?`,
		now.Format(time.RFC3339Nano), sess.ID)
	if err != nil {
		// Losing this is harmless; the session is still valid either way.
		return
	}
}

// Delete ends one session. This is both logout and lock.
func (s *Sessions) Delete(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteExpired removes dead rows. Called at startup: cheap, and it keeps the
// table from growing without bound on a machine that runs for years.
func (s *Sessions) DeleteExpired(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ?`,
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// hashToken is what turns a cookie value into a row id.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewCookie builds the cookie for a freshly created session.
//
// Secure is not set. The architecture's decision is plain HTTP over the station
// LAN — there is no certificate authority on a Philippine buying station's
// network and a self-signed certificate would train staff to click through
// browser warnings, which is worse than the LAN exposure it would prevent. A
// Secure cookie over that plain-HTTP origin would simply never be sent, so
// setting it would break login rather than harden it. Revisit together with
// that decision, not separately.
//
// SameSite=Lax blocks the cross-site POST that would otherwise let a page on
// another tab post a payment as whoever is logged in.
func NewCookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// ClearCookie expires the cookie in the browser. Sent on logout and on lock, so
// a locked station does not keep a dead token around to be confusing later.
func ClearCookie() *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}
