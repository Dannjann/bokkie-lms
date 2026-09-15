// Package api serves the HTTP surface: the JSON API, the health endpoint, and
// (from task 1.6) the embedded Vue SPA.
//
// It owns routing and middleware only. Process lifecycle — listening, draining,
// shutting down — belongs to internal/app, because the service manager in task
// 1.7 needs to drive that without going through a command line.
package api

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"
)

// Deps is everything the handlers need. Passing it explicitly keeps handlers
// testable against an in-memory database with no server running.
type Deps struct {
	Logger  *slog.Logger
	DB      *sql.DB
	Started time.Time
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

	// Placeholder root until task 1.6 embeds the SPA.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bokkie is running; the app shell arrives in task 1.6", http.StatusOK)
	})

	return withRequestID(
		withRequestLog(d.Logger,
			withPanicRecovery(d.Logger,
				withSession(mux))))
}
