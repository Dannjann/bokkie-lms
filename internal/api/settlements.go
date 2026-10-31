package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Dannjann/bokkie-lms/internal/domain/settlement"
)

// The settlement's seven routes.
//
// A settlement is counter work, as it was in the legacy system: a farmer has
// delivered and is standing there to be settled up. Reading, computing and
// posting are therefore open to encoders. Voiding is a manager's, as voiding
// every other document is — it is how money leaves the record.

func handleListSettlements(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		status := settlement.Status(strings.TrimSpace(q.Get("status")))
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

		page, err := d.Settlements.List(r.Context(), settlement.Query{
			Text:       q.Get("q"),
			CustomerID: int64(atoiOr(q.Get("customer_id"), 0)),
			Status:     status,
			From:       from,
			To:         to,
			Page:       atoiOr(q.Get("page"), 0),
			PerPage:    atoiOr(q.Get("per_page"), 0),
		})
		if err != nil {
			d.Logger.Error("list settlements", "id", RequestID(r.Context()), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(r.Context())})
			return
		}
		writeJSON(w, http.StatusOK, page)
	}
}

// settlementDetail is one settlement and what it did to the ledger — including
// the reversals a void added, which is how the screen shows that a settlement
// was undone rather than that it never happened.
type settlementDetail struct {
	Settlement settlement.Settlement `json:"settlement"`
	Entries    []entryView           `json:"entries"`
}

func handleGetSettlement(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		id, err := settlementID(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: err.Error(), RequestID: RequestID(ctx)})
			return
		}

		st, err := d.Settlements.ByID(ctx, id)
		if err != nil {
			respondSettlementError(w, r, d, err, "read a settlement")
			return
		}

		entries, err := d.Settlements.Entries(ctx, id)
		if err != nil {
			respondSettlementError(w, r, d, err, "read what a settlement charged")
			return
		}

		writeJSON(w, http.StatusOK, settlementDetail{Settlement: st, Entries: entryViewsOf(entries)})
	}
}

// handleCreateSettlement computes a settlement and saves it as a draft.
//
// This is the preview, and there is no separate endpoint for one: the draft is
// the settlement minus the commit. The body is a settlement.Draft — a farmer, a
// cut-off, a rate and a note, and nothing else: no lines, no number, no status.
// A caller that could supply lines could charge an arbitrary farmer an
// arbitrary amount, and decodeJSON refuses unknown fields so it cannot try.
func handleCreateSettlement(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		var draft settlement.Draft
		if err := decodeJSON(r, &draft); err != nil {
			return Change{}, err
		}

		st, err := d.Settlements.CreateTx(ctx, tx, draft, settlementActorOf(r))
		if err != nil {
			return Change{}, settlementError(err)
		}
		return Change{
			EntityID: settlementEntityID(st),
			After:    st,
			Status:   http.StatusCreated,
			Body:     st,
		}, nil
	}
}

// handleUpdateSettlement recomputes a draft — with a different cut-off or
// rate, or with the same ones after the record has moved.
func handleUpdateSettlement(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := settlementID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		var draft settlement.Draft
		if err := decodeJSON(r, &draft); err != nil {
			return Change{}, err
		}

		before, err := d.Settlements.ByID(ctx, id)
		if err != nil {
			return Change{}, settlementError(err)
		}
		after, err := d.Settlements.UpdateTx(ctx, tx, id, draft)
		if err != nil {
			return Change{}, settlementError(err)
		}
		return Change{EntityID: settlementEntityID(after), Before: before, After: after, Body: after}, nil
	}
}

func handleDeleteSettlement(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := settlementID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		before, err := d.Settlements.ByID(ctx, id)
		if err != nil {
			return Change{}, settlementError(err)
		}
		if err := d.Settlements.DeleteTx(ctx, tx, id); err != nil {
			return Change{}, settlementError(err)
		}
		return Change{
			EntityID: settlementEntityID(before),
			Before:   before,
			Status:   http.StatusNoContent,
		}, nil
	}
}

// handlePostSettlement charges the interest and applies the payments. Its
// refusals — a stale preview, no payment to apply — are 409s carrying the
// domain's sentence.
func handlePostSettlement(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := settlementID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		// Read before the transaction writes anything, on the pool's own
		// connection: PostTx needs its UPDATE to be the first statement *this
		// transaction* runs, and a read outside it holds no lock to upgrade.
		before, err := d.Settlements.ByID(ctx, id)
		if err != nil {
			return Change{}, settlementError(err)
		}

		after, err := d.Settlements.PostTx(ctx, tx, id, settlementActorOf(r))
		if err != nil {
			return Change{}, settlementError(err)
		}
		return Change{EntityID: settlementEntityID(after), Before: before, After: after, Body: after}, nil
	}
}

// handleVoidSettlement reverses everything a posted settlement wrote.
func handleVoidSettlement(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := settlementID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		var req voidRequest
		if err := decodeJSON(r, &req); err != nil {
			return Change{}, err
		}

		before, err := d.Settlements.ByID(ctx, id)
		if err != nil {
			return Change{}, settlementError(err)
		}

		after, err := d.Settlements.VoidTx(ctx, tx, id, settlementActorOf(r), req.Reason)
		if err != nil {
			return Change{}, settlementError(err)
		}
		return Change{EntityID: settlementEntityID(after), Before: before, After: after, Body: after}, nil
	}
}

// settlementActorOf is who is making the request.
func settlementActorOf(r *http.Request) settlement.Actor {
	user, ok := CurrentUser(r.Context())
	if !ok {
		return settlement.Actor{}
	}
	return settlement.Actor{UserID: user.ID, Username: user.Username}
}

// settlementEntityID is what the audit log files this event under: the id rather
// than the number, because a settlement has to be traceable across every event in
// its life and it has no number for the first half of it.
func settlementEntityID(st settlement.Settlement) string { return strconv.FormatInt(st.ID, 10) }

func settlementID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("that is not a liquidation id")
	}
	return id, nil
}

// settlementError maps the domain's errors onto status codes. Anything it does
// not recognise is left alone, which the router turns into a 500 and a log
// line — the fault Step 6's statement handler was corrected for.
func settlementError(err error) error {
	if se, ok := asSettlementError(err); ok {
		return se
	}
	return err
}

func asSettlementError(err error) (statusError, bool) {
	var ve settlement.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest("%s", ve.Message), true
	case errors.Is(err, settlement.ErrNotFound):
		return statusError{http.StatusNotFound, "no such liquidation"}, true
	case errors.Is(err, settlement.ErrNotDraft), errors.Is(err, settlement.ErrNotPosted):
		// 409 rather than 400: nothing about the request is malformed, the settlement
		// simply moved on — which is what a second click looks like.
		return conflict("%s", err.Error()), true
	case errors.Is(err, settlement.ErrNotLatest),
		errors.Is(err, settlement.ErrPreviewStale),
		errors.Is(err, settlement.ErrNoPayment):
		// Also 409, carrying the sentence: which figure moved, or which
		// settlement has to be voided first.
		return conflict("%s", err.Error()), true
	default:
		return statusError{}, false
	}
}

func respondSettlementError(w http.ResponseWriter, r *http.Request, d Deps, err error, doing string) {
	if se, ok := asSettlementError(err); ok {
		writeJSON(w, se.status, errorBody{Error: se.message, RequestID: RequestID(r.Context())})
		return
	}
	d.Logger.Error(doing, "id", RequestID(r.Context()), "error", err)
	writeJSON(w, http.StatusInternalServerError, errorBody{
		Error: "internal error", RequestID: RequestID(r.Context())})
}
