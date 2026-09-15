package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

// contextKey is unexported so nothing outside this package can collide with it.
type contextKey int

const requestIDKey contextKey = iota

// RequestIDHeader is echoed back on every response, so a screenshot of a failed
// request is enough to find the matching log lines.
const RequestIDHeader = "X-Request-Id"

// RequestID returns the ID assigned to r, or "" outside the middleware chain.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Only used for correlation, never for security. A clock-based ID is a
		// perfectly good fallback and is not worth failing a request over.
		return time.Now().UTC().Format("20060102150405.000000")
	}
	return hex.EncodeToString(b[:])
}

// withRequestLog records one line per request.
//
// The path is logged without its query string on purpose: a customer search
// puts a person's name in the query, and these log files get copied onto USB
// sticks. Identifiers, not people.
func withRequestLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		log.Info("request",
			"id", RequestID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.written,
			"duration_ms", time.Since(started).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

// withPanicRecovery turns a panic into a 500 rather than taking the process
// down. One bad request must never stop the station taking payments.
func withPanicRecovery(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if recovered == http.ErrAbortHandler {
				panic(recovered) // the server's own signal; let it through
			}
			log.Error("panic serving request",
				"id", RequestID(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"panic", recovered,
				"stack", stackTrace(),
			)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error:     "internal error",
				RequestID: RequestID(r.Context()),
			})
		}()

		next.ServeHTTP(w, r)
	})
}

// withSession is the hook task 2.1 fills in with cookie-based authentication.
// It exists now so that the chain it has to slot into is already proven, and so
// adding auth is not also a refactor of the middleware order.
func withSession(next http.Handler) http.Handler {
	return next
}

// statusRecorder captures what was actually sent, for the log line.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.written += n
	return n, err
}
