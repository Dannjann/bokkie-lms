package api

import (
	"encoding/json"
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
