package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"
)

// auditEntryView is one row as the UI shows it. before_json and after_json are
// passed through as raw text rather than re-parsed: they are a record of what
// was written, and re-encoding them through a Go type would quietly rewrite
// history to match today's struct definitions.
type auditEntryView struct {
	ID        int64   `json:"id"`
	At        string  `json:"at"`
	UserID    *int64  `json:"user_id,omitempty"`
	Username  string  `json:"username"`
	Action    string  `json:"action"`
	Entity    string  `json:"entity,omitempty"`
	EntityID  string  `json:"entity_id,omitempty"`
	Before    *string `json:"before,omitempty"`
	After     *string `json:"after,omitempty"`
	RequestID string  `json:"request_id,omitempty"`
	IP        string  `json:"ip,omitempty"`
}

// auditPageSize caps one page. The table is never purged, so by year three
// there is a lot of it and an unbounded query would be a way to stall the
// station from a browser.
const auditPageSize = 100

// handleListAudit reads the audit log, newest first.
//
// There is no write path here, and there never will be. The table's append-only
// nature is enforced by triggers in the schema as well, so even a hand-typed
// UPDATE in a sqlite3 shell is refused — see 00002_identity.sql.
func handleListAudit(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Keyset pagination on the id, not OFFSET: entries are only ever
		// appended, so "everything older than the last one I saw" is stable
		// even while new entries arrive mid-scroll.
		before := int64(0)
		if raw := r.URL.Query().Get("before"); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, errorBody{
					Error: "before must be an audit entry id", RequestID: RequestID(ctx)})
				return
			}
			before = n
		}

		query := `SELECT id, at, user_id, username, action, entity, entity_id,
		                 before_json, after_json, request_id, ip
		            FROM audit_log`
		args := []any{}
		if before > 0 {
			query += ` WHERE id < ?`
			args = append(args, before)
		}
		query += ` ORDER BY id DESC LIMIT ?`
		args = append(args, auditPageSize)

		rows, err := d.DB.QueryContext(ctx, query, args...)
		if err != nil {
			d.Logger.Error("read audit log", "id", RequestID(ctx), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}
		defer rows.Close()

		loc := d.Settings.Location()
		entries := []auditEntryView{}
		for rows.Next() {
			var (
				view                  auditEntryView
				at                    string
				userID                sql.NullInt64
				beforeJSON, afterJSON sql.NullString
			)
			if err := rows.Scan(&view.ID, &at, &userID, &view.Username, &view.Action,
				&view.Entity, &view.EntityID, &beforeJSON, &afterJSON,
				&view.RequestID, &view.IP); err != nil {
				d.Logger.Error("read audit log", "id", RequestID(ctx), "error", err)
				writeJSON(w, http.StatusInternalServerError, errorBody{
					Error: "internal error", RequestID: RequestID(ctx)})
				return
			}

			// Stored UTC, shown in the station's timezone. An audit screen
			// showing times three hours off from the wall clock is worse than
			// useless when someone is reconstructing what happened.
			if t, err := time.Parse(time.RFC3339Nano, at); err == nil {
				view.At = t.In(loc).Format(time.RFC3339)
			} else {
				view.At = at
			}
			if userID.Valid {
				view.UserID = &userID.Int64
			}
			if beforeJSON.Valid {
				view.Before = &beforeJSON.String
			}
			if afterJSON.Valid {
				view.After = &afterJSON.String
			}
			entries = append(entries, view)
		}
		if err := rows.Err(); err != nil {
			d.Logger.Error("read audit log", "id", RequestID(ctx), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}

		var next int64
		if len(entries) == auditPageSize {
			next = entries[len(entries)-1].ID
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"entries": entries,
			"next":    next,
		})
	}
}
