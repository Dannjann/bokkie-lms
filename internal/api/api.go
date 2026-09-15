// Package api serves the HTTP surface: the JSON API, the health endpoint, and
// the embedded Vue SPA.
//
// It owns routing and middleware only. Process lifecycle — listening, draining,
// shutting down — belongs to internal/app, because the service manager needs to
// drive that without going through a command line.
//
// Routes are registered through the small registry in routes.go rather than
// directly on a ServeMux. That buys two guarantees that are otherwise a matter
// of everyone remembering: every route states its access level at registration,
// so a handler cannot forget a check it never had the chance to write; and
// every mutating route states how it is audited, so a route that changes stored
// state without leaving a trace cannot be registered at all.
package api

import (
	"database/sql"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	"github.com/Dannjann/bokkie-lms/internal/auth"
	"github.com/Dannjann/bokkie-lms/internal/backup"
	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// Deps is everything the handlers need. Passing it explicitly keeps handlers
// testable against an in-memory database with no server running.
type Deps struct {
	Logger  *slog.Logger
	DB      *sql.DB
	Started time.Time

	Users    *auth.Users
	Sessions *auth.Sessions
	Limiter  *auth.Limiter
	Audit    *audit.Log
	Settings *settings.Store

	// Customers and Ledger are held side by side rather than one inside the
	// other. A customer's balance is composed from both at the handler — see
	// customerDetail — which is what lets neither domain package import the
	// other.
	Customers *customer.Service
	Ledger    *ledger.Service

	// Backup runs the on-demand backup. It is the same scheduler that runs the
	// nightly job, so a manual backup and a scheduled one are literally the
	// same code path.
	Backup       *backup.Scheduler
	BackupFolder string

	// SPA is the built frontend, embedded in the binary. Ignored when DevProxy
	// is set.
	SPA fs.FS

	// DevProxy, when set, forwards everything that is not API to the Vite dev
	// server instead of serving SPA. This is the one flag that switches
	// between development and production serving.
	DevProxy *url.URL
}

// Handler is the built router plus the routes it registered. The route list is
// exported so the coverage test can enumerate what was registered rather than
// trusting a hand-maintained list to stay in step with the code.
type Handler struct {
	http.Handler
	Routes []Route
}

// NewHandler builds the router and wraps it in the middleware chain.
//
// The chain is ordered outermost first: every request gets an ID before
// anything can log it, the log line is written even for a request that panics,
// the panic recovery sits close enough to the handler that a panic cannot
// escape into the server's own goroutine, and the session is resolved last so
// it is inside the recovery — a malformed cookie must not take the process
// down.
func NewHandler(d Deps) *Handler {
	rt := newRouter(d)
	registerRoutes(rt, d)

	mux := rt.mux
	mux.HandleFunc("GET /healthz", healthHandler(d))

	// Unknown /api paths must answer as JSON. Falling through to the SPA shell
	// here produces a client error that reads as "the API returned HTML",
	// which is a baffling thing to debug from a counter PC.
	mux.HandleFunc("/api/", notFoundJSON)

	// Everything else is the app: the embedded SPA in production, or the Vite
	// dev server in development. Both are reached on bokkie's own port, so
	// there is one origin either way.
	//
	// The SPA shell is served without an access check on purpose. It is a
	// static bundle with nothing confidential in it, and the API behind it is
	// what actually refuses — gating the shell would only mean the login page
	// could not load.
	if d.DevProxy != nil {
		mux.Handle("/", newDevProxy(d.DevProxy, d.Logger))
	} else {
		mux.Handle("/", newSPAHandler(d.SPA))
	}

	handler := withRequestID(
		withRequestLog(d.Logger,
			withPanicRecovery(d.Logger,
				withSession(d, mux))))

	return &Handler{Handler: handler, Routes: rt.routes}
}

// registerRoutes is the whole API surface in one readable block.
//
// Keeping it in one function is deliberate: the access level of every endpoint
// in the system can be reviewed by reading forty lines, which is not true of
// handlers that register themselves from a dozen files.
func registerRoutes(rt *router, d Deps) {
	// --- Identity -----------------------------------------------------------
	//
	// These four are the only Public routes in the system, and each is public
	// for a reason that cannot be designed away: you cannot authenticate to
	// find out whether you need to authenticate.

	rt.read(Public, "/api/session", handleSession(d))

	rt.mutateSelfAudited(Public, http.MethodPost, "/api/session",
		"records login success, failure and lockout separately — a wrapper that only "+
			"audited success would miss every event this endpoint exists to trace",
		handleLogin(d))

	rt.mutateSelfAudited(Public, http.MethodPost, "/api/setup",
		"creates the first user and starts their session; its refusal on an "+
			"already-configured install is itself the entry worth keeping",
		handleSetup(d))

	rt.mutateSelfAudited(Authenticated, http.MethodDelete, "/api/session",
		"ends the session and clears the cookie; the audit action differs between "+
			"logout and lock",
		handleLogout(d))

	rt.mutateSelfAudited(Authenticated, http.MethodPost, "/api/session/lock",
		"ends the session but remembers the username for the login form",
		handleLock(d))

	// --- Accounts -----------------------------------------------------------
	//
	// Manager-only: who has access to the ledger is a management question.

	rt.read(ManagerOnly, "/api/users", handleListUsers(d))
	rt.mutate(ManagerOnly, http.MethodPost, "/api/users", "user.created", "user", handleCreateUser(d))

	// --- Settings -----------------------------------------------------------
	//
	// Readable by anyone logged in — the slip layout and the timezone are
	// needed to render screens — but writable only by a manager.

	rt.read(Authenticated, "/api/settings", handleListSettings(d))
	rt.mutate(ManagerOnly, http.MethodPut, "/api/settings", "setting.changed", "setting", handleUpdateSetting(d))

	// --- Backup -------------------------------------------------------------

	rt.read(ManagerOnly, "/api/backups", handleListBackups(d))
	rt.mutateOutsideTx(ManagerOnly, http.MethodPost, "/api/backups", "backup.created", "backup",
		handleBackupNow(d))

	// --- Customers ----------------------------------------------------------
	//
	// Reading and writing a customer record is counter work: an encoder looks
	// people up all day and types in a new farmer while they are standing
	// there. Only the two irreversible-ish operations are held back.

	rt.read(Authenticated, "/api/customers", handleListCustomers(d))
	rt.read(Authenticated, "/api/customers/{id}", handleGetCustomer(d))

	rt.mutate(Authenticated, http.MethodPost, "/api/customers",
		"customer.created", "customer", handleCreateCustomer(d))
	rt.mutate(Authenticated, http.MethodPut, "/api/customers/{id}",
		"customer.updated", "customer", handleUpdateCustomer(d))

	// Nothing is ever deleted: codes are allocated as a high-water mark, so
	// releasing one would hand a former customer's code to a new one. This is
	// the nearest thing to a deletion the system has, so it is a manager's.
	rt.mutate(ManagerOnly, http.MethodPost, "/api/customers/{id}/active",
		"customer.active.changed", "customer", handleSetCustomerActive(d))

	rt.mutateSelfAudited(ManagerOnly, http.MethodPost, "/api/customers/import",
		auditImportRoute, handleImportCustomers(d))

	// --- Audit --------------------------------------------------------------
	//
	// Manager-only, and read-only for everyone including managers: the table
	// has no update or delete path in code, and triggers in the schema refuse
	// one at the database.

	rt.read(ManagerOnly, "/api/audit", handleListAudit(d))
}
