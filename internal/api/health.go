package api

import (
	"context"
	"net/http"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/buildinfo"
)

// healthPingTimeout bounds the database check. /healthz is what the service
// manager and whoever is standing at the machine poll, so it must answer
// quickly even when SQLite is busy.
const healthPingTimeout = 2 * time.Second

type healthResponse struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	Database      string `json:"database"`
}

// healthHandler reports whether this process can actually do its job, which
// means reaching the database — a server that answers HTTP while the database
// is unreachable is down, and saying "ok" would hide that.
func healthHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthPingTimeout)
		defer cancel()

		body := healthResponse{
			Status:        "ok",
			Version:       buildinfo.Version,
			Commit:        buildinfo.Commit,
			UptimeSeconds: int64(time.Since(d.Started).Seconds()),
			Database:      "ok",
		}
		status := http.StatusOK

		if err := d.DB.PingContext(ctx); err != nil {
			body.Status, body.Database = "degraded", "unreachable"
			status = http.StatusServiceUnavailable
			d.Logger.Error("health check: database unreachable",
				"id", RequestID(r.Context()), "error", err)
		}

		writeJSON(w, status, body)
	}
}
