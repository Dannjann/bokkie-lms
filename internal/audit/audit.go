// Package audit records who did what, when.
//
// The table is append-only and is never purged. This is a financial record for
// a lending business: the value of an old entry is precisely at the moment
// someone disputes something old, which is the moment a retention policy would
// have deleted it.
//
// The design rule this package exists to enforce is that **the easy path is the
// audited path**. Writing an audited mutation must not be more work than
// writing an unaudited one, because if it is, one handler will eventually skip
// it and nobody will be able to tell which. See internal/api/routes.go, where a
// mutating route cannot be registered without naming the action recorded here.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Action is what happened. These are the security events every system needs
// regardless of domain; domain actions ("advance.post", "payment.void") are
// named at the route that performs them.
const (
	ActionLoginSucceeded = "auth.login.succeeded"
	ActionLoginFailed    = "auth.login.failed"
	ActionLoginBlocked   = "auth.login.blocked"
	ActionLogout         = "auth.logout"
	ActionLock           = "auth.lock"
	ActionAccessDenied   = "auth.access.denied"
	ActionSetupCompleted = "setup.completed"

	// ActionSetupRefused is a POST to the first-run setup endpoint on an
	// install that already has an administrator. It is either a confused
	// operator or someone trying their luck, and both are worth a row.
	ActionSetupRefused = "setup.refused"
)

// Entry is one thing that happened. Everything except Action is optional,
// because the useful entries are not all shaped alike: a failed login has a
// username but no user, a settings change has before and after but no entity
// id worth the name.
type Entry struct {
	At        time.Time
	UserID    int64  // 0 when nobody was authenticated
	Username  string // recorded even when UserID is 0
	Action    string
	Entity    string
	EntityID  string
	Before    any // marshalled to before_json; nil writes SQL NULL
	After     any // marshalled to after_json
	RequestID string
	IP        string
}

// Recorder writes entries. It is an interface so a handler under test can be
// handed one that collects in memory, and so the route wrapper can depend on
// the behaviour rather than on a *sql.DB.
type Recorder interface {
	Record(ctx context.Context, e Entry) error
}

// Log is the database-backed Recorder.
type Log struct {
	db  *sql.DB
	log *slog.Logger
}

// New returns a Recorder writing to database.
func New(database *sql.DB, log *slog.Logger) *Log {
	return &Log{db: database, log: log}
}

// Record appends one entry.
//
// It takes the same context as the request that caused it, so an audit write is
// cancelled with its request rather than outliving it. Callers inside a
// transaction should use RecordTx instead, so the entry and the change it
// describes commit or roll back together.
func (l *Log) Record(ctx context.Context, e Entry) error {
	return l.exec(ctx, l.db, e)
}

// RecordTx appends an entry inside an existing transaction. This is how a
// domain mutation and its audit entry become one atomic fact: a posted voucher
// with no audit trail, or an audit entry for a voucher that was rolled back,
// are both worse than either failing outright.
func (l *Log) RecordTx(ctx context.Context, tx *sql.Tx, e Entry) error {
	return l.exec(ctx, tx, e)
}

// execer is what Record and RecordTx have in common.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (l *Log) exec(ctx context.Context, db execer, e Entry) error {
	if e.Action == "" {
		return fmt.Errorf("audit entry has no action")
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}

	before, err := encode(e.Before)
	if err != nil {
		return fmt.Errorf("audit %s: encode before: %w", e.Action, err)
	}
	after, err := encode(e.After)
	if err != nil {
		return fmt.Errorf("audit %s: encode after: %w", e.Action, err)
	}

	var userID any
	if e.UserID != 0 {
		userID = e.UserID
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO audit_log
		   (at, user_id, username, action, entity, entity_id, before_json, after_json, request_id, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.At.UTC().Format(time.RFC3339Nano), userID, e.Username, e.Action,
		e.Entity, e.EntityID, before, after, e.RequestID, e.IP)
	if err != nil {
		return fmt.Errorf("audit %s: %w", e.Action, err)
	}
	return nil
}

// encode renders a before/after snapshot, or SQL NULL for nothing. A nil
// interface and a typed nil both mean "there was no such state".
func encode(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if string(data) == "null" {
		return nil, nil
	}
	return string(data), nil
}

// RecordOrLog writes an entry and, if that fails, says so loudly instead of
// returning an error.
//
// It is for the entries that describe something that has already happened and
// cannot be undone — a refused request, a failed login. Failing the response
// because the audit write failed would turn a logging fault into an outage, and
// the request is over either way. Mutations must never use this: there the
// entry and the change belong in one transaction, so use RecordTx.
func (l *Log) RecordOrLog(ctx context.Context, e Entry) {
	if err := l.Record(ctx, e); err != nil {
		l.log.Error("could not write audit entry",
			"action", e.Action, "user", e.Username, "request_id", e.RequestID, "error", err)
	}
}
