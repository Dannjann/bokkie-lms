package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
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

// auditFilter is the narrowing a request asked for. Every field is optional and
// an empty one is left out of the SQL entirely rather than matched against a
// wildcard — "no filter" and "filter on nothing" must not become the same query.
type auditFilter struct {
	userID  int64
	action  string
	entity  string
	from    string // inclusive boundary, as a sortable prefix; see auditDayBound
	to      string // exclusive boundary, the start of the day after `to`
	before  int64  // keyset cursor: only entries older than this id
	hasUser bool
}

// where builds the WHERE clause and its arguments.
func (f auditFilter) where() (string, []any) {
	clauses := []string{}
	args := []any{}

	if f.before > 0 {
		clauses = append(clauses, "id < ?")
		args = append(args, f.before)
	}
	if f.hasUser {
		clauses = append(clauses, "user_id = ?")
		args = append(args, f.userID)
	}
	if f.action != "" {
		// Equality, never LIKE. In SQL LIKE a dot is literal, so the risk is
		// not the one it looks like — it is `_`, which matches any single
		// character, and LIKE's ASCII case-insensitivity. A filter on
		// "user_created" would quietly also return "user.created", and one on
		// "AUTH.LOGIN.FAILED" would return the lowercase rows. Neither is what
		// a person reconstructing an incident is asking for.
		clauses = append(clauses, "action = ?")
		args = append(args, f.action)
	}
	if f.entity != "" {
		clauses = append(clauses, "entity = ?")
		args = append(args, f.entity)
	}
	if f.from != "" {
		clauses = append(clauses, "at >= ?")
		args = append(args, f.from)
	}
	if f.to != "" {
		clauses = append(clauses, "at < ?")
		args = append(args, f.to)
	}

	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// auditDayBound turns a date typed on the screen into a string that compares
// correctly against the `at` column.
//
// `at` is written with time.RFC3339Nano, which **drops trailing zeros from the
// fractional second**. So the same instant can be stored as
// "2026-09-18T00:00:00Z" or "2026-09-18T00:00:00.123Z", and comparing those
// against a full RFC3339 boundary lexicographically is wrong: '.' sorts before
// 'Z', so an entry 123 milliseconds into the range would test as *earlier* than
// the range's own midnight and be dropped.
//
// The boundary is therefore truncated to whole seconds and left without a zone
// suffix. Every stored value that begins with those nineteen characters sorts
// after it, whatever fraction and suffix follow, which is exactly the
// comparison wanted.
//
// The date itself is read in the station's timezone, because that is the day
// the person at the counter means, and then converted to UTC to match storage.
func auditDayBound(day time.Time, loc *time.Location) string {
	midnight := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	return midnight.UTC().Format("2006-01-02T15:04:05")
}

// parseAuditFilter reads the query string, or explains what is wrong with it.
func parseAuditFilter(r *http.Request, loc *time.Location) (auditFilter, error) {
	q := r.URL.Query()
	var f auditFilter

	if raw := q.Get("before"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return f, badRequest("before must be an audit entry id")
		}
		f.before = n
	}
	if raw := q.Get("user_id"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return f, badRequest("user_id must be a user id")
		}
		f.userID, f.hasUser = n, true
	}

	f.action = strings.TrimSpace(q.Get("action"))
	f.entity = strings.TrimSpace(q.Get("entity"))

	from, to := strings.TrimSpace(q.Get("from")), strings.TrimSpace(q.Get("to"))
	var fromDay, toDay time.Time
	if from != "" {
		day, err := time.ParseInLocation("2006-01-02", from, loc)
		if err != nil {
			return f, badRequest("from must be a date, as 2026-03-02")
		}
		fromDay = day
		f.from = auditDayBound(day, loc)
	}
	if to != "" {
		day, err := time.ParseInLocation("2006-01-02", to, loc)
		if err != nil {
			return f, badRequest("to must be a date, as 2026-03-02")
		}
		toDay = day
		// Exclusive, at the start of the following day — which is how both
		// ends come out inclusive without needing to know what the last
		// instant of a day looks like.
		f.to = auditDayBound(day.AddDate(0, 0, 1), loc)
	}
	if from != "" && to != "" && toDay.Before(fromDay) {
		return f, badRequest("the period runs backwards: %s is before %s", to, from)
	}

	return f, nil
}

// handleListAudit reads the audit log, newest first, narrowed by whatever the
// screen asked for.
//
// There is no write path here, and there never will be. The table's append-only
// nature is enforced by triggers in the schema as well, so even a hand-typed
// UPDATE in a sqlite3 shell is refused — see 00002_identity.sql.
func handleListAudit(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		loc := d.Settings.Location()

		filter, err := parseAuditFilter(r, loc)
		if err != nil {
			var se statusError
			if errors.As(err, &se) {
				writeJSON(w, se.status, errorBody{Error: se.message, RequestID: RequestID(ctx)})
				return
			}
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: err.Error(), RequestID: RequestID(ctx)})
			return
		}

		// Keyset pagination on the id, not OFFSET: entries are only ever
		// appended, so "everything older than the last one I saw" is stable
		// even while new entries arrive mid-scroll — and it stays stable with
		// a filter on, because the filter narrows the same monotonic sequence.
		where, args := filter.where()
		query := `SELECT id, at, user_id, username, action, entity, entity_id,
		                 before_json, after_json, request_id, ip
		            FROM audit_log` + where + ` ORDER BY id DESC LIMIT ?`
		args = append(args, auditPageSize)

		rows, err := d.DB.QueryContext(ctx, query, args...)
		if err != nil {
			d.Logger.Error("read audit log", "id", RequestID(ctx), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}
		defer rows.Close()

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

// auditFacets is what the filter bar offers to narrow by.
type auditFacets struct {
	Users    []auditFacetUser `json:"users"`
	Actions  []string         `json:"actions"`
	Entities []string         `json:"entities"`
}

type auditFacetUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// handleAuditFacets lists the users, actions and entities **present in the
// table**.
//
// Deliberately read from the data rather than from a list written down
// somewhere. The action strings live partly in internal/audit's constants and
// partly in the rt.mutate call sites in api.go; a third list would drift from
// both, and the drift would show up as a filter option that matches nothing or
// an action nobody can filter for. What has happened is exactly what is worth
// offering to narrow by.
//
// It also means a deleted-from-the-roster user — which cannot happen today,
// accounts only deactivate — would still be filterable for as long as their
// entries exist, which is for ever.
func handleAuditFacets(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		facets := auditFacets{Users: []auditFacetUser{}, Actions: []string{}, Entities: []string{}}

		if err := scanStrings(ctx, d.DB,
			`SELECT DISTINCT action FROM audit_log ORDER BY action`, &facets.Actions); err != nil {
			auditFacetsFailed(w, r, d, err)
			return
		}
		if err := scanStrings(ctx, d.DB,
			`SELECT DISTINCT entity FROM audit_log WHERE entity <> '' ORDER BY entity`,
			&facets.Entities); err != nil {
			auditFacetsFailed(w, r, d, err)
			return
		}

		// Grouped by id, not by username: two entries can carry the same
		// username at different ids only if an account were recreated, and the
		// filter is on user_id because that is the indexed column.
		rows, err := d.DB.QueryContext(ctx,
			`SELECT user_id, min(username) FROM audit_log
			  WHERE user_id IS NOT NULL GROUP BY user_id ORDER BY min(username)`)
		if err != nil {
			auditFacetsFailed(w, r, d, err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var u auditFacetUser
			if err := rows.Scan(&u.ID, &u.Username); err != nil {
				auditFacetsFailed(w, r, d, err)
				return
			}
			facets.Users = append(facets.Users, u)
		}
		if err := rows.Err(); err != nil {
			auditFacetsFailed(w, r, d, err)
			return
		}

		writeJSON(w, http.StatusOK, facets)
	}
}

func scanStrings(ctx context.Context, db *sql.DB, query string, into *[]string) error {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return err
		}
		*into = append(*into, s)
	}
	return rows.Err()
}

func auditFacetsFailed(w http.ResponseWriter, r *http.Request, d Deps, err error) {
	d.Logger.Error("read audit facets", "id", RequestID(r.Context()), "error", err)
	writeJSON(w, http.StatusInternalServerError, errorBody{
		Error: "internal error", RequestID: RequestID(r.Context())})
}
