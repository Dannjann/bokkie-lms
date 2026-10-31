package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Dannjann/bokkie-lms/internal/domain/advance"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
)

// handleListAdvances is the voucher list.
//
// The status filter defaults to all rather than to posted: an unfinished draft
// is something somebody needs to see, not something to go looking for.
func handleListAdvances(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		status := advance.Status(strings.TrimSpace(q.Get("status")))
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

		page, err := d.Advances.List(r.Context(), advance.Query{
			Text:    q.Get("q"),
			Status:  status,
			From:    from,
			To:      to,
			Page:    atoiOr(q.Get("page"), 0),
			PerPage: atoiOr(q.Get("per_page"), 0),
		})
		if err != nil {
			d.Logger.Error("list advances", "id", RequestID(r.Context()), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(r.Context())})
			return
		}
		writeJSON(w, http.StatusOK, page)
	}
}

// advanceDetail is one voucher plus what it did to the ledger.
//
// The two halves are composed here, the same way a customer's detail composes
// the master record with a balance: the advance package knows what the document
// says, and the ledger knows what is actually on the record because of it. A
// posted voucher's screen shows both, and after a void the entries are where
// the reversals appear.
type advanceDetail struct {
	Voucher advance.Voucher `json:"voucher"`
	Entries []entryView     `json:"entries"`
}

// entryView is a ledger entry as a screen shows it. Money marshals as a string
// through money.Money already; the rest is flattened so the client never has to
// know the ledger's internal shape.
type entryView struct {
	ID         int64       `json:"id"`
	CustomerID int64       `json:"customer_id"`
	Date       ledger.Date `json:"date"`
	Type       string      `json:"type"`
	Principal  string      `json:"principal"`
	Interest   string      `json:"interest"`
	Reason     string      `json:"reason"`
	ReversesID int64       `json:"reverses_id"`
	By         string      `json:"by"`
}

func entryViewsOf(entries []ledger.Entry) []entryView {
	views := make([]entryView, 0, len(entries))
	for _, e := range entries {
		views = append(views, entryView{
			ID:         e.ID,
			CustomerID: e.CustomerID,
			Date:       e.Date,
			Type:       string(e.Type),
			Principal:  e.Principal.String(),
			Interest:   e.Interest.String(),
			Reason:     e.Reason,
			ReversesID: e.ReversesID,
			By:         e.By.Username,
		})
	}
	return views
}

func handleGetAdvance(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := advanceID(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: err.Error(), RequestID: RequestID(r.Context())})
			return
		}

		v, err := d.Advances.ByID(r.Context(), id)
		if err != nil {
			respondAdvanceError(w, r, d, err, "read a voucher")
			return
		}

		entries, err := d.Advances.Entries(r.Context(), id)
		if err != nil {
			respondAdvanceError(w, r, d, err, "read what a voucher posted")
			return
		}
		writeJSON(w, http.StatusOK, advanceDetail{Voucher: v, Entries: entryViewsOf(entries)})
	}
}

// handleCreateAdvance starts a draft.
//
// Open to encoders: entering an advance at the counter is the everyday work
// this system exists for. The body is an advance.Draft, which carries no Ref.
// No. and no status — so a caller cannot supply either even by trying, because
// decodeJSON refuses unknown fields.
func handleCreateAdvance(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		var draft advance.Draft
		if err := decodeJSON(r, &draft); err != nil {
			return Change{}, err
		}

		v, err := d.Advances.CreateTx(ctx, tx, draft, actorOf(r))
		if err != nil {
			return Change{}, advanceError(err)
		}
		return Change{
			EntityID: voucherEntityID(v),
			After:    v,
			Status:   http.StatusCreated,
			Body:     v,
		}, nil
	}
}

// handleCreateAndPostAdvance enters a slip and posts it in one go, for the
// encoder who has no reason to stop at a draft.
//
// One transaction, so a slip that fails to post — a line that does not hold up
// — leaves no draft behind for somebody to find later. The INSERT is this
// transaction's first statement, which is what PostTx needs of it.
func handleCreateAndPostAdvance(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		var draft advance.Draft
		if err := decodeJSON(r, &draft); err != nil {
			return Change{}, err
		}

		created, err := d.Advances.CreateTx(ctx, tx, draft, actorOf(r))
		if err != nil {
			return Change{}, advanceError(err)
		}
		posted, err := d.Advances.PostTx(ctx, tx, created.ID, actorOf(r))
		if err != nil {
			return Change{}, advanceError(err)
		}
		return Change{
			EntityID: voucherEntityID(posted),
			After:    posted,
			Status:   http.StatusCreated,
			Body:     posted,
		}, nil
	}
}

func handleUpdateAdvance(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := advanceID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		var draft advance.Draft
		if err := decodeJSON(r, &draft); err != nil {
			return Change{}, err
		}

		before, err := d.Advances.ByID(ctx, id)
		if err != nil {
			return Change{}, advanceError(err)
		}
		after, err := d.Advances.UpdateTx(ctx, tx, id, draft)
		if err != nil {
			return Change{}, advanceError(err)
		}
		return Change{EntityID: voucherEntityID(after), Before: before, After: after, Body: after}, nil
	}
}

// handleDeleteAdvance removes a draft.
//
// This is the only endpoint in the system that deletes anything, and it is open
// to encoders for the reason that makes it uninteresting: a draft moved no
// money. What it said is in the audit entry's before_json, which is where a
// deleted draft should be looked for.
func handleDeleteAdvance(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := advanceID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		before, err := d.Advances.ByID(ctx, id)
		if err != nil {
			return Change{}, advanceError(err)
		}
		if err := d.Advances.DeleteTx(ctx, tx, id); err != nil {
			return Change{}, advanceError(err)
		}
		return Change{
			EntityID: voucherEntityID(before),
			Before:   before,
			Status:   http.StatusNoContent,
		}, nil
	}
}

// handlePostAdvance turns a draft into money owed.
//
// Open to encoders, because this is the counter's job — a farmer is standing
// there and the slip is being written. Undoing it is not: that is the void
// below, and it is a manager's.
func handlePostAdvance(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := advanceID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		// Read before the transaction writes anything, on the pool's own
		// connection: PostTx needs its UPDATE to be the first statement *this
		// transaction* runs, and a read outside it holds no lock to upgrade.
		before, err := d.Advances.ByID(ctx, id)
		if err != nil {
			return Change{}, advanceError(err)
		}

		after, err := d.Advances.PostTx(ctx, tx, id, actorOf(r))
		if err != nil {
			return Change{}, advanceError(err)
		}
		return Change{EntityID: voucherEntityID(after), Before: before, After: after, Body: after}, nil
	}
}

// handleEditAdvance edits a posted slip by correcting it: the original is
// voided and a replacement posted under the same Ref. No., in one transaction.
// See advance.CorrectTx.
//
// Open to everyone who can post, by the client's decision: an edit is in the
// audit log with its before and after, and on the slip as "Edited by".
func handleEditAdvance(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := advanceID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}
		var draft advance.Draft
		if err := decodeJSON(r, &draft); err != nil {
			return Change{}, err
		}

		// Read on the pool, before the transaction writes: CorrectTx needs its
		// INSERT to be the first statement this transaction runs.
		before, err := d.Advances.ByID(ctx, id)
		if err != nil {
			return Change{}, advanceError(err)
		}
		after, err := d.Advances.CorrectTx(ctx, tx, id, draft, actorOf(r))
		if err != nil {
			return Change{}, advanceError(err)
		}
		return Change{EntityID: voucherEntityID(after), Before: before, After: after, Body: after}, nil
	}
}

type voidRequest struct {
	Reason string `json:"reason"`
}

// handleVoidAdvance reverses everything a posted voucher did.
//
// Manager-only: this is the only way money leaves the record, and it is the
// operation 00-architecture.md singles out as a manager's in its Security
// section.
func handleVoidAdvance(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := advanceID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		var req voidRequest
		if err := decodeJSON(r, &req); err != nil {
			return Change{}, err
		}

		before, err := d.Advances.ByID(ctx, id)
		if err != nil {
			return Change{}, advanceError(err)
		}

		after, err := d.Advances.VoidTx(ctx, tx, id, actorOf(r), req.Reason)
		if err != nil {
			return Change{}, advanceError(err)
		}
		return Change{EntityID: voucherEntityID(after), Before: before, After: after, Body: after}, nil
	}
}

// actorOf is who is making the request. Every one of these routes is
// authenticated, so the session is always there; the zero Actor would be
// refused by the domain package anyway, which is the belt to this brace.
func actorOf(r *http.Request) advance.Actor {
	user, ok := CurrentUser(r.Context())
	if !ok {
		return advance.Actor{}
	}
	return advance.Actor{UserID: user.ID, Username: user.Username}
}

// voucherEntityID is what the audit log files this event under.
//
// The id rather than the Ref. No., because a voucher has to be traceable across
// every event in its life and it has no number for the first half of it. The
// Ref. No. is in the after_json of the post entry, where it appears at the
// moment it is assigned.
func voucherEntityID(v advance.Voucher) string { return strconv.FormatInt(v.ID, 10) }

func advanceID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("that is not an advance slip id")
	}
	return id, nil
}

func optionalDate(text string) (ledger.Date, error) {
	if strings.TrimSpace(text) == "" {
		return ledger.Date{}, nil
	}
	return ledger.ParseDate(text)
}

// advanceError maps the domain's errors onto status codes. Anything it does not
// recognise is left alone, which the router turns into a 500 and a log line.
func advanceError(err error) error {
	if se, ok := asAdvanceError(err); ok {
		return se
	}
	return err
}

func asAdvanceError(err error) (statusError, bool) {
	var ve advance.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest("%s", ve.Message), true
	case errors.Is(err, advance.ErrNotFound):
		return statusError{http.StatusNotFound, "no such advance slip"}, true
	case errors.Is(err, advance.ErrNotDraft):
		// 409 rather than 400: nothing about the request is malformed, the
		// voucher simply moved on — which is what a second click looks like.
		return conflict("%s", err.Error()), true
	case errors.Is(err, advance.ErrNotPosted), errors.Is(err, advance.ErrSettled),
		errors.Is(err, advance.ErrNotEditable):
		return conflict("%s", err.Error()), true
	default:
		return statusError{}, false
	}
}

func respondAdvanceError(w http.ResponseWriter, r *http.Request, d Deps, err error, doing string) {
	if se, ok := asAdvanceError(err); ok {
		writeJSON(w, se.status, errorBody{Error: se.message, RequestID: RequestID(r.Context())})
		return
	}
	d.Logger.Error(doing, "id", RequestID(r.Context()), "error", err)
	writeJSON(w, http.StatusInternalServerError, errorBody{
		Error: "internal error", RequestID: RequestID(r.Context())})
}
