package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	"github.com/Dannjann/bokkie-lms/internal/auth"
)

// Access is what a route requires of the caller.
type Access int

const (
	// Public is reachable without logging in. Only login and the first-run
	// setup flow should ever be Public.
	Public Access = iota

	// Authenticated is any active logged-in user.
	Authenticated

	// ManagerOnly is the gate on destructive operations. Step 5's "void a
	// posted voucher" — the most sensitive action in the system — attaches
	// here, which is why the gate is built before there is anything to gate.
	ManagerOnly
)

func (a Access) String() string {
	switch a {
	case Authenticated:
		return "authenticated"
	case ManagerOnly:
		return "manager"
	default:
		return "public"
	}
}

// auditStyle records how a route's audit entry is written, so the coverage test
// can tell "audited" from "nobody got round to it".
type auditStyle int

const (
	auditNone      auditStyle = iota // read-only routes
	auditByWrapper                   // the registration wrapper writes it
	auditBySelf                      // the handler writes richer entries itself
)

// Route is one registered endpoint. The registry of these is what the coverage
// test in routes_test.go enumerates: the guarantee is structural — a mutating
// route cannot be registered without choosing an audit style — rather than a
// matter of every handler author remembering.
type Route struct {
	Method  string
	Pattern string
	Access  Access
	Action  string // the audit action, for wrapper-audited routes
	Entity  string

	audit auditStyle
	why   string // for auditBySelf: why the wrapper is not doing it
}

// Mutating reports whether this route can change stored state. Method is the
// whole test, which is exactly why every mutating route uses a mutating method
// and no read uses one.
func (r Route) Mutating() bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// Audited reports whether an entry lands in audit_log when this route runs.
func (r Route) Audited() bool { return r.audit != auditNone }

// Change is what a mutating handler reports back: what it changed, for the
// audit entry, and what to send the caller.
//
// Returning this instead of writing the response directly is the mechanism that
// makes the audited path the easy path — the handler does less work than an
// unaudited one would, not more.
type Change struct {
	EntityID string
	Before   any // state before, marshalled to before_json; nil for a creation
	After    any // state after; nil for a deletion

	Status int // response status; 200 when zero
	Body   any // response body; no body when nil

	// AfterCommit runs once the transaction has committed, for the work that
	// must not happen until the change is durable — reloading a cache, say.
	// A failure here is logged, not returned: the change is already committed
	// and reporting an error would tell the caller it did not happen.
	AfterCommit func(context.Context) error
}

// TxHandler is a mutating handler that runs inside a transaction. The audit
// entry is written into the same transaction, so a change without its audit
// trail, or an audit entry for a change that rolled back, are both impossible.
type TxHandler func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error)

// NonTxHandler is a mutating handler that cannot run in a transaction — SQLite
// refuses VACUUM INTO inside one, for instance. Its audit entry is written
// immediately after it succeeds.
type NonTxHandler func(ctx context.Context, r *http.Request) (Change, error)

// router accumulates routes as they are registered, wrapping each in the
// access check and the audit write its registration implies.
type router struct {
	mux    *http.ServeMux
	deps   Deps
	routes []Route
}

func newRouter(d Deps) *router {
	return &router{mux: http.NewServeMux(), deps: d}
}

// read registers a route that changes nothing.
func (rt *router) read(access Access, pattern string, h http.HandlerFunc) {
	rt.register(Route{Method: http.MethodGet, Pattern: pattern, Access: access, audit: auditNone},
		rt.guard(access, h))
}

// mutate registers the default kind of mutating route: transactional, audited
// in the same transaction. Reach for this one unless it genuinely cannot work.
func (rt *router) mutate(access Access, method, pattern, action, entity string, h TxHandler) {
	rt.register(
		Route{Method: method, Pattern: pattern, Access: access, Action: action, Entity: entity, audit: auditByWrapper},
		rt.guard(access, rt.runTx(action, entity, h)))
}

// mutateOutsideTx registers a mutating route for an operation SQLite cannot run
// inside a transaction. The audit entry is written straight after the handler
// succeeds, which leaves a narrow window where the operation happened and the
// entry did not — so use mutate unless that is impossible.
func (rt *router) mutateOutsideTx(access Access, method, pattern, action, entity string, h NonTxHandler) {
	rt.register(
		Route{Method: method, Pattern: pattern, Access: access, Action: action, Entity: entity, audit: auditByWrapper},
		rt.guard(access, rt.run(action, entity, h)))
}

// mutateSelfAudited registers a mutating route whose handler writes its own
// audit entries. The reason is recorded on the route and printed by the
// coverage test, so this stays a deliberate, visible exception rather than a
// quiet hole.
func (rt *router) mutateSelfAudited(access Access, method, pattern, why string, h http.HandlerFunc) {
	rt.register(
		Route{Method: method, Pattern: pattern, Access: access, audit: auditBySelf, why: why},
		rt.guard(access, h))
}

func (rt *router) register(route Route, h http.Handler) {
	rt.routes = append(rt.routes, route)
	rt.mux.Handle(route.Method+" "+route.Pattern, h)
}

// guard applies the access check.
//
// It is applied here, at registration, rather than inside the handler. That is
// the whole point: a handler cannot forget a check it never had the chance to
// write, and adding a route without deciding its access is a compile error
// because Access is a required argument.
func (rt *router) guard(access Access, next http.HandlerFunc) http.HandlerFunc {
	if access == Public {
		return next
	}

	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := CurrentUser(r.Context())
		if !ok {
			// Not an audit event: an anonymous request to a protected route is
			// almost always an expired session, and recording every one would
			// bury the entries that matter.
			unauthorized(w, r)
			return
		}
		if access == ManagerOnly && user.Role != auth.RoleManager {
			rt.denied(w, r, user)
			return
		}
		next(w, r)
	}
}

// denied refuses a request and records it. A refusal is a security event, not
// just a status code: "the encoder tried to void a voucher" is exactly the sort
// of thing the owner will want to see, and the request log alone does not say
// who it was.
func (rt *router) denied(w http.ResponseWriter, r *http.Request, user auth.User) {
	rt.deps.Audit.RecordOrLog(r.Context(), audit.Entry{
		UserID:    user.ID,
		Username:  user.Username,
		Action:    audit.ActionAccessDenied,
		Entity:    "route",
		EntityID:  r.Method + " " + r.URL.Path,
		RequestID: RequestID(r.Context()),
		IP:        clientIP(r),
	})

	rt.deps.Logger.Warn("access denied",
		"id", RequestID(r.Context()),
		"user", user.Username,
		"role", user.Role,
		"method", r.Method,
		"path", r.URL.Path,
	)

	writeJSON(w, http.StatusForbidden, errorBody{
		Error:     "this action is restricted to managers",
		RequestID: RequestID(r.Context()),
	})
}

// runTx is the transactional mutating path: begin, run the handler, write the
// audit entry into the same transaction, commit.
func (rt *router) runTx(action, entity string, h TxHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		tx, err := rt.deps.DB.BeginTx(ctx, nil)
		if err != nil {
			rt.fail(w, r, fmt.Errorf("begin transaction: %w", err))
			return
		}
		defer tx.Rollback()

		change, err := h(ctx, tx, r)
		if err != nil {
			rt.fail(w, r, err)
			return
		}

		if err := rt.deps.Audit.RecordTx(ctx, tx, rt.entry(r, action, entity, change)); err != nil {
			rt.fail(w, r, err)
			return
		}
		if err := tx.Commit(); err != nil {
			rt.fail(w, r, fmt.Errorf("commit: %w", err))
			return
		}

		rt.afterCommit(ctx, action, change)
		respondChange(w, change)
	}
}

// run is the non-transactional mutating path.
func (rt *router) run(action, entity string, h NonTxHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		change, err := h(ctx, r)
		if err != nil {
			rt.fail(w, r, err)
			return
		}

		// The operation already happened, so a failed audit write cannot be
		// undone by refusing the response. It is reported as an error anyway:
		// an unaudited mutation is a fault worth someone noticing, and the
		// caller can check whether the operation took effect.
		if err := rt.deps.Audit.Record(ctx, rt.entry(r, action, entity, change)); err != nil {
			rt.deps.Logger.Error("mutation completed but its audit entry was not written",
				"id", RequestID(ctx), "action", action, "error", err)
			rt.fail(w, r, fmt.Errorf("the operation completed but could not be recorded in the audit log: %w", err))
			return
		}

		rt.afterCommit(ctx, action, change)
		respondChange(w, change)
	}
}

func (rt *router) afterCommit(ctx context.Context, action string, change Change) {
	if change.AfterCommit == nil {
		return
	}
	if err := change.AfterCommit(ctx); err != nil {
		rt.deps.Logger.Error("post-commit step failed after a successful change",
			"id", RequestID(ctx), "action", action, "error", err)
	}
}

func (rt *router) entry(r *http.Request, action, entity string, change Change) audit.Entry {
	e := audit.Entry{
		Action:    action,
		Entity:    entity,
		EntityID:  change.EntityID,
		Before:    change.Before,
		After:     change.After,
		RequestID: RequestID(r.Context()),
		IP:        clientIP(r),
	}
	if user, ok := CurrentUser(r.Context()); ok {
		e.UserID, e.Username = user.ID, user.Username
	}
	return e
}

func respondChange(w http.ResponseWriter, change Change) {
	status := change.Status
	if status == 0 {
		status = http.StatusOK
	}
	if change.Body == nil {
		w.WriteHeader(status)
		return
	}
	writeJSON(w, status, change.Body)
}

// fail turns a handler error into a response. A statusError carries a message
// meant for whoever is standing at the counter; anything else is a fault, and
// its detail stays in the log where it cannot leak schema or paths to a client.
func (rt *router) fail(w http.ResponseWriter, r *http.Request, err error) {
	var se statusError
	if errors.As(err, &se) {
		writeJSON(w, se.status, errorBody{Error: se.message, RequestID: RequestID(r.Context())})
		return
	}

	rt.deps.Logger.Error("request failed",
		"id", RequestID(r.Context()),
		"method", r.Method,
		"path", r.URL.Path,
		"error", err,
	)
	writeJSON(w, http.StatusInternalServerError, errorBody{
		Error:     "internal error",
		RequestID: RequestID(r.Context()),
	})
}

func unauthorized(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusUnauthorized, errorBody{
		Error:     "please log in",
		RequestID: RequestID(r.Context()),
	})
}
