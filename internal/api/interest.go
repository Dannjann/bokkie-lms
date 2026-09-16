package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Dannjann/bokkie-lms/internal/domain/interest"
)

// The interest run's seven routes.
//
// Every one of them is ManagerOnly, reads included, and that is the one place
// this section departs from the shape advances and payments share. Everything
// else in the system opens its everyday half to encoders because a farmer is
// standing at the counter and the work is theirs. Nothing about a run is
// counter work: nobody is standing there, it is not typed off a slip, and it
// changes what every farmer owes at once. Reading is held back with the rest
// because the preview *is* the dangerous artifact — it is the screen that ends
// in a Post button.

func handleListInterestRuns(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		status := interest.Status(strings.TrimSpace(q.Get("status")))
		if q.Get("status") != "" && !status.Valid() {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error:     "status must be draft, posted or voided",
				RequestID: RequestID(r.Context()),
			})
			return
		}

		from, err := optionalDate(q.Get("from"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "from must be a date, as 2026-03-02", RequestID: RequestID(r.Context())})
			return
		}
		to, err := optionalDate(q.Get("to"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "to must be a date, as 2026-03-02", RequestID: RequestID(r.Context())})
			return
		}

		page, err := d.Interest.List(r.Context(), interest.Query{
			Text:    q.Get("q"),
			Status:  status,
			From:    from,
			To:      to,
			Page:    atoiOr(q.Get("page"), 0),
			PerPage: atoiOr(q.Get("per_page"), 0),
		})
		if err != nil {
			d.Logger.Error("list interest runs", "id", RequestID(r.Context()), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(r.Context())})
			return
		}
		writeJSON(w, http.StatusOK, page)
	}
}

// interestDetail is one run, its lines, and what it did to the ledger.
//
// The lines come with the run. The entries are separate and are what a posted
// run actually wrote — including the reversals a void added, which is how the
// screen shows that a run was undone rather than that it never happened.
type interestDetail struct {
	Run     interest.Run `json:"run"`
	Entries []entryView  `json:"entries"`
}

func handleGetInterestRun(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		id, err := interestID(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: err.Error(), RequestID: RequestID(ctx)})
			return
		}

		run, err := d.Interest.ByID(ctx, id)
		if err != nil {
			respondInterestError(w, r, d, err, "read an interest run")
			return
		}

		entries, err := d.Interest.Entries(ctx, id)
		if err != nil {
			respondInterestError(w, r, d, err, "read what an interest run charged")
			return
		}

		writeJSON(w, http.StatusOK, interestDetail{Run: run, Entries: entryViewsOf(entries)})
	}
}

// handleCreateInterestRun computes a run and saves it as a draft.
//
// This is the preview, and there is no separate endpoint for one: the draft is
// the run minus the commit. The body is an interest.Draft, which carries a
// period and a note and nothing else — no rule, no lines, no number, no status.
// A caller that could supply lines could charge an arbitrary farmer an
// arbitrary amount, and decodeJSON refuses unknown fields so it cannot try.
func handleCreateInterestRun(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		var draft interest.Draft
		if err := decodeJSON(r, &draft); err != nil {
			return Change{}, err
		}

		run, err := d.Interest.CreateTx(ctx, tx, draft, interestActorOf(r))
		if err != nil {
			return Change{}, interestError(err)
		}
		return Change{
			EntityID: interestEntityID(run),
			After:    run,
			Status:   http.StatusCreated,
			Body:     run,
		}, nil
	}
}

// handleUpdateInterestRun recomputes a draft — against a different period, or
// against the same one after the ledger has moved. It is also how a run
// computed under the wrong rate is corrected: change the setting, recompute.
func handleUpdateInterestRun(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := interestID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		var draft interest.Draft
		if err := decodeJSON(r, &draft); err != nil {
			return Change{}, err
		}

		before, err := d.Interest.ByID(ctx, id)
		if err != nil {
			return Change{}, interestError(err)
		}
		after, err := d.Interest.UpdateTx(ctx, tx, id, draft)
		if err != nil {
			return Change{}, interestError(err)
		}
		return Change{EntityID: interestEntityID(after), Before: before, After: after, Body: after}, nil
	}
}

func handleDeleteInterestRun(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := interestID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		before, err := d.Interest.ByID(ctx, id)
		if err != nil {
			return Change{}, interestError(err)
		}
		if err := d.Interest.DeleteTx(ctx, tx, id); err != nil {
			return Change{}, interestError(err)
		}
		return Change{
			EntityID: interestEntityID(before),
			Before:   before,
			Status:   http.StatusNoContent,
		}, nil
	}
}

// handlePostInterestRun charges the run.
//
// The most consequential route in the system: one click, one entry per
// borrower. It can refuse for three reasons that are not the caller's fault —
// the period is already charged, the preview is stale, the run charges nothing
// — and each of those is a 409 carrying the sentence the domain wrote, because
// the sentence is the whole value of the refusal.
func handlePostInterestRun(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := interestID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		// Read before the transaction writes anything, on the pool's own
		// connection: PostTx needs its UPDATE to be the first statement *this
		// transaction* runs, and a read outside it holds no lock to upgrade.
		before, err := d.Interest.ByID(ctx, id)
		if err != nil {
			return Change{}, interestError(err)
		}

		after, err := d.Interest.PostTx(ctx, tx, id, interestActorOf(r))
		if err != nil {
			return Change{}, interestError(err)
		}
		return Change{EntityID: interestEntityID(after), Before: before, After: after, Body: after}, nil
	}
}

// handleVoidInterestRun reverses everything a posted run charged.
func handleVoidInterestRun(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := interestID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		var req voidRequest
		if err := decodeJSON(r, &req); err != nil {
			return Change{}, err
		}

		before, err := d.Interest.ByID(ctx, id)
		if err != nil {
			return Change{}, interestError(err)
		}

		after, err := d.Interest.VoidTx(ctx, tx, id, interestActorOf(r), req.Reason)
		if err != nil {
			return Change{}, interestError(err)
		}
		return Change{EntityID: interestEntityID(after), Before: before, After: after, Body: after}, nil
	}
}

// interestActorOf is who is making the request.
func interestActorOf(r *http.Request) interest.Actor {
	user, ok := CurrentUser(r.Context())
	if !ok {
		return interest.Actor{}
	}
	return interest.Actor{UserID: user.ID, Username: user.Username}
}

// interestEntityID is what the audit log files this event under: the id rather
// than the run number, because a run has to be traceable across every event in
// its life and it has no number for the first half of it.
func interestEntityID(run interest.Run) string { return strconv.FormatInt(run.ID, 10) }

func interestID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("that is not an interest run id")
	}
	return id, nil
}

// interestError maps the domain's errors onto status codes. Anything it does
// not recognise is left alone, which the router turns into a 500 and a log
// line — the fault Step 6's statement handler was corrected for.
func interestError(err error) error {
	if se, ok := asInterestError(err); ok {
		return se
	}
	return err
}

func asInterestError(err error) (statusError, bool) {
	var ve interest.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest("%s", ve.Message), true
	case errors.Is(err, interest.ErrNotFound):
		return statusError{http.StatusNotFound, "no such interest run"}, true
	case errors.Is(err, interest.ErrNotDraft), errors.Is(err, interest.ErrNotPosted):
		// 409 rather than 400: nothing about the request is malformed, the run
		// simply moved on — which is what a second click looks like.
		return conflict("%s", err.Error()), true
	case errors.Is(err, interest.ErrPeriodAlreadyCharged),
		errors.Is(err, interest.ErrPreviewStale),
		errors.Is(err, interest.ErrNothingToCharge):
		// Also 409, and the sentence matters more here than anywhere else in
		// the API: it names the run that already covers the period, or the
		// farmer whose figure moved. A generic "conflict" would send somebody
		// looking in the wrong place.
		return conflict("%s", err.Error()), true
	default:
		return statusError{}, false
	}
}

func respondInterestError(w http.ResponseWriter, r *http.Request, d Deps, err error, doing string) {
	if se, ok := asInterestError(err); ok {
		writeJSON(w, se.status, errorBody{Error: se.message, RequestID: RequestID(r.Context())})
		return
	}
	d.Logger.Error(doing, "id", RequestID(r.Context()), "error", err)
	writeJSON(w, http.StatusInternalServerError, errorBody{
		Error: "internal error", RequestID: RequestID(r.Context())})
}
