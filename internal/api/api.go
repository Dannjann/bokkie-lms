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
	"github.com/Dannjann/bokkie-lms/internal/domain/advance"
	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
	"github.com/Dannjann/bokkie-lms/internal/domain/interest"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/payment"
	"github.com/Dannjann/bokkie-lms/internal/domain/report"
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

	// Advances is the voucher: the one document that writes to the ledger.
	// Unlike the two above it holds the ledger itself, because posting a
	// voucher *is* a ledger write and a voucher service without one could not
	// do the only thing it exists for.
	Advances *advance.Service

	// Payments is the receipt: the other document that writes to the ledger,
	// and the only one that writes entries in the other direction.
	Payments *payment.Service

	// Interest is the run: the third document, and the only one that writes to
	// every borrower's balance at once rather than to one farmer's.
	Interest *interest.Service

	// Reports is the only one of these that cannot write. It has no
	// transaction, no INSERT and no method that takes one, which is Q4 of the
	// domain interview answered in code rather than in a comment.
	Reports *report.Service

	// Backup runs the on-demand backup. It is the same scheduler that runs the
	// nightly job, so a manual backup and a scheduled one are literally the
	// same code path.
	Backup       *backup.Scheduler
	BackupFolder string

	// Discovery publishes the station's LAN name. The API holds it so that
	// switching LAN access off can withdraw the name in the same breath, rather
	// than leaving the network pointed at a server that will now refuse it.
	//
	// It is an interface, and nil is a legitimate value: a test changing a
	// setting must not need a working mDNS responder, and an install with mDNS
	// switched off in config.yaml has no name to withdraw.
	Discovery Discovery

	// SPA is the built frontend, embedded in the binary. Ignored when DevProxy
	// is set.
	SPA fs.FS

	// DevProxy, when set, forwards everything that is not API to the Vite dev
	// server instead of serving SPA. This is the one flag that switches
	// between development and production serving.
	DevProxy *url.URL
}

// Discovery is the part of LAN-name publishing the API needs: the ability to
// bring the name up or take it down. Keeping it this narrow is what lets
// internal/api stay ignorant of mDNS, which is three platform-specific files
// nothing here should have an opinion about.
type Discovery interface {
	SetPublished(published bool) error
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
// escape into the server's own goroutine, the network gate decides whether this
// machine is allowed in at all before any work is done on its behalf, and the
// session is resolved last so it is inside the recovery — a malformed cookie
// must not take the process down.
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
				withNetworkGate(d,
					withSession(d, mux)))))

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

	// The three transitions. Each takes effect on the target's next request —
	// withSession re-reads the account every time — except the password, which
	// ends the account's sessions itself because a changed hash does not.
	//
	// "user.activated" is the registered default and the handler overrides it
	// with "user.deactivated" on the way off, so the audit screen's action
	// filter can tell the two apart.
	rt.mutate(ManagerOnly, http.MethodPost, "/api/users/{id}/active",
		"user.activated", "user", handleSetUserActive(d))
	rt.mutate(ManagerOnly, http.MethodPost, "/api/users/{id}/role",
		"user.role.changed", "user", handleSetUserRole(d))
	rt.mutate(ManagerOnly, http.MethodPost, "/api/users/{id}/password",
		"user.password.reset", "user", handleResetUserPassword(d))

	// Changing your own password is not a management action and is not gated
	// on one. It hangs off the session rather than off /api/users/{id} because
	// it identifies its target by who is asking, and an endpoint that takes an
	// id invites somebody to pass a different one.
	rt.mutate(Authenticated, http.MethodPost, "/api/session/password",
		"user.password.changed", "user", handleChangeOwnPassword(d))

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

	// --- Advances -----------------------------------------------------------
	//
	// Entering an advance is counter work, the same as adding a farmer: a
	// farmer is standing there and a slip is being written. Posting is part of
	// that work and so is open to encoders. Voiding is not — it is the only way
	// money leaves the record, and it is a manager's.
	//
	// Deleting is the one deletion in the system, and it is open to encoders
	// for the reason that makes it uninteresting: a draft moved no money, and
	// what it said survives in the audit entry's before_json.

	rt.read(Authenticated, "/api/advances", handleListAdvances(d))
	rt.read(Authenticated, "/api/advances/{id}", handleGetAdvance(d))

	rt.mutate(Authenticated, http.MethodPost, "/api/advances",
		"advance.draft.created", "advance", handleCreateAdvance(d))
	rt.mutate(Authenticated, http.MethodPut, "/api/advances/{id}",
		"advance.draft.updated", "advance", handleUpdateAdvance(d))
	rt.mutate(Authenticated, http.MethodDelete, "/api/advances/{id}",
		"advance.draft.deleted", "advance", handleDeleteAdvance(d))

	rt.mutate(Authenticated, http.MethodPost, "/api/advances/{id}/post",
		"advance.posted", "advance", handlePostAdvance(d))

	rt.mutate(ManagerOnly, http.MethodPost, "/api/advances/{id}/void",
		"advance.voided", "advance", handleVoidAdvance(d))

	// --- Payments -----------------------------------------------------------
	//
	// The same shape as the advances above, and for the same reasons. Taking a
	// payment is counter work: a farmer is standing there with cash or has just
	// delivered palay, and posting is part of that work. Voiding is not — with
	// voiding a voucher it is one of the two ways money leaves the record.

	rt.read(Authenticated, "/api/payments", handleListPayments(d))
	rt.read(Authenticated, "/api/payments/{id}", handleGetPayment(d))

	rt.mutate(Authenticated, http.MethodPost, "/api/payments",
		"payment.draft.created", "payment", handleCreatePayment(d))
	rt.mutate(Authenticated, http.MethodPut, "/api/payments/{id}",
		"payment.draft.updated", "payment", handleUpdatePayment(d))
	rt.mutate(Authenticated, http.MethodDelete, "/api/payments/{id}",
		"payment.draft.deleted", "payment", handleDeletePayment(d))

	rt.mutate(Authenticated, http.MethodPost, "/api/payments/{id}/post",
		"payment.posted", "payment", handlePostPayment(d))

	rt.mutate(ManagerOnly, http.MethodPost, "/api/payments/{id}/void",
		"payment.voided", "payment", handleVoidPayment(d))

	// --- Interest -----------------------------------------------------------
	//
	// ManagerOnly throughout, reads included, and that is the one place this
	// section departs from the two above. Everything else opens its everyday
	// half to encoders because a farmer is standing at the counter and the work
	// is theirs. Nothing about a run is counter work — nobody is standing
	// there, it is not typed off a slip, and it changes what every farmer owes
	// at once. The preview is held back with the rest because the preview is
	// the dangerous artifact: it is the screen that ends in a Post button.

	rt.read(ManagerOnly, "/api/interest-runs", handleListInterestRuns(d))
	rt.read(ManagerOnly, "/api/interest-runs/{id}", handleGetInterestRun(d))

	rt.mutate(ManagerOnly, http.MethodPost, "/api/interest-runs",
		"interest.draft.created", "interest", handleCreateInterestRun(d))
	rt.mutate(ManagerOnly, http.MethodPut, "/api/interest-runs/{id}",
		"interest.draft.updated", "interest", handleUpdateInterestRun(d))
	rt.mutate(ManagerOnly, http.MethodDelete, "/api/interest-runs/{id}",
		"interest.draft.deleted", "interest", handleDeleteInterestRun(d))

	rt.mutate(ManagerOnly, http.MethodPost, "/api/interest-runs/{id}/post",
		"interest.posted", "interest", handlePostInterestRun(d))

	rt.mutate(ManagerOnly, http.MethodPost, "/api/interest-runs/{id}/void",
		"interest.voided", "interest", handleVoidInterestRun(d))

	// The statement hangs off the customer rather than off the ledger, because
	// that is the question being asked: what does this farmer owe, and why.
	rt.read(Authenticated, "/api/customers/{id}/statement", handleGetStatement(d))

	// --- Reports ------------------------------------------------------------
	//
	// Portfolio views, so manager-only like the interest run. What an encoder
	// needs about one farmer is on the customer screen and on the statement,
	// both of which are already open to them.
	//
	// Read-only, and not merely by convention: the package behind these has no
	// write path at all.

	rt.read(ManagerOnly, "/api/reports/current-advances", handleCurrentAdvances(d))
	rt.read(ManagerOnly, "/api/reports/roi", handleROI(d))
	rt.read(ManagerOnly, "/api/reports/aging", handleAging(d))
	rt.read(ManagerOnly, "/api/reports/liquidated", handleLiquidated(d))

	// --- Audit --------------------------------------------------------------
	//
	// Manager-only, and read-only for everyone including managers: the table
	// has no update or delete path in code, and triggers in the schema refuse
	// one at the database.

	rt.read(ManagerOnly, "/api/audit", handleListAudit(d))
}
