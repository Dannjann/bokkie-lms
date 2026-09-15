// Package api serves the HTTP surface: the JSON API, the health endpoint, and
// (from task 1.6) the embedded Vue SPA.
//
// It owns routing and middleware only. Process lifecycle — listening, draining,
// shutting down — belongs to internal/app, because the service manager in task
// 1.7 needs to drive that without going through a command line.
package api

import (
	"database/sql"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// Deps is everything the handlers need. Passing it explicitly keeps handlers
// testable against an in-memory database with no server running.
type Deps struct {
	Logger  *slog.Logger
	DB      *sql.DB
	Started time.Time

	// SPA is the built frontend, embedded in the binary. Ignored when DevProxy
	// is set.
	SPA fs.FS

	// DevProxy, when set, forwards everything that is not API to the Vite dev
	// server instead of serving SPA. This is the one flag that switches
	// between development and production serving.
	DevProxy *url.URL
}

// NewHandler builds the router and wraps it in the middleware chain.
//
// The chain is ordered outermost first: every request gets an ID before
// anything can log it, the log line is written even for a request that panics,
// and the panic recovery sits close enough to the handler that a panic cannot
// escape into the server's own goroutine.
func NewHandler(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthHandler(d))

	// Unknown /api paths must answer as JSON. Falling through to the SPA shell
	// here produces a client error that reads as "the API returned HTML",
	// which is a baffling thing to debug from a counter PC.
	mux.HandleFunc("/api/", notFoundJSON)

	// Everything else is the app: the embedded SPA in production, or the Vite
	// dev server in development. Both are reached on bokkie's own port, so
	// there is one origin either way.
	if d.DevProxy != nil {
		mux.Handle("/", newDevProxy(d.DevProxy, d.Logger))
	} else {
		mux.Handle("/", newSPAHandler(d.SPA))
	}

	return withRequestID(
		withRequestLog(d.Logger,
			withPanicRecovery(d.Logger,
				withSession(mux))))
}
