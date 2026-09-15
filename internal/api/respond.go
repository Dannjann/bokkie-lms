package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
)

// errorBody is the shape of every error the API returns. The request ID is
// included so an operator can read it off the screen and it can be found in the
// log without asking them what time it happened.
type errorBody struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already sent, so there is nothing to tell the
		// client. Recording it is all that is left.
		slog.Error("write response body", "error", err)
	}
}

func notFoundJSON(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, errorBody{
		Error:     "no such endpoint",
		RequestID: RequestID(r.Context()),
	})
}

// stackTrace captures the goroutine that panicked, capped so a runaway stack
// cannot fill the log file.
func stackTrace() string {
	buf := make([]byte, 8<<10)
	return string(buf[:runtime.Stack(buf, false)])
}

// statusError is an error with a status code and a message that is safe, and
// useful, to show the person at the counter. Anything that is not one of these
// is a fault: it becomes a 500 and its detail stays in the log.
type statusError struct {
	status  int
	message string
}

func (e statusError) Error() string { return e.message }

func badRequest(format string, args ...any) statusError {
	return statusError{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

func conflict(format string, args ...any) statusError {
	return statusError{http.StatusConflict, fmt.Sprintf(format, args...)}
}

func tooManyRequests(format string, args ...any) statusError {
	return statusError{http.StatusTooManyRequests, fmt.Sprintf(format, args...)}
}

// decodeJSON reads a request body into v, rejecting unknown fields so a
// misspelled key is a visible error rather than a value that silently never
// arrived.
func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxRequestBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return badRequest("could not read the request: %v", err)
	}
	return nil
}

// maxRequestBody caps what a handler will read. Nothing this API accepts is
// large, and an unbounded read is a way to exhaust a station PC's memory.
const maxRequestBody = 1 << 20
