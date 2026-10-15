package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Dannjann/bokkie-lms/internal/domain/payment"
)

// handleListPayments is the payment list.
//
// The status filter defaults to all rather than to posted, for the reason the
// voucher list does: an unfinished draft is something somebody needs to see,
// not something to go looking for.
func handleListPayments(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		status := payment.Status(strings.TrimSpace(q.Get("status")))
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

		page, err := d.Payments.List(r.Context(), payment.Query{
			Text:       q.Get("q"),
			CustomerID: int64(atoiOr(q.Get("customer_id"), 0)),
			Status:     status,
			From:       from,
			To:         to,
			Page:       atoiOr(q.Get("page"), 0),
			PerPage:    atoiOr(q.Get("per_page"), 0),
		})
		if err != nil {
			d.Logger.Error("list payments", "id", RequestID(r.Context()), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(r.Context())})
			return
		}
		writeJSON(w, http.StatusOK, page)
	}
}

// paymentDetail is one receipt, what it did to the ledger, and what the
// customer owes now.
//
// The entries are where the split between principal and interest is read from:
// the payments table does not store it, deliberately, because a second copy of
// a number is a number that can disagree with the record.
//
// The balance is here because the screen that posts a payment needs it — to
// show what is owed beside the amount being typed, and to warn before an
// overpayment rather than after it.
type paymentDetail struct {
	Payment payment.Payment `json:"payment"`
	Entries []entryView     `json:"entries"`
	Balance balanceView     `json:"balance"`
}

func handleGetPayment(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		id, err := paymentID(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: err.Error(), RequestID: RequestID(ctx)})
			return
		}

		p, err := d.Payments.ByID(ctx, id)
		if err != nil {
			respondPaymentError(w, r, d, err, "read a payment")
			return
		}

		entries, err := d.Payments.Entries(ctx, id)
		if err != nil {
			respondPaymentError(w, r, d, err, "read what a payment posted")
			return
		}

		balance, err := d.Ledger.BalanceFor(ctx, p.CustomerID)
		if err != nil {
			respondPaymentError(w, r, d, err, "read what a customer owes")
			return
		}

		writeJSON(w, http.StatusOK, paymentDetail{
			Payment: p,
			Entries: entryViewsOf(entries),
			Balance: viewOf(balance),
		})
	}
}

// handleCreatePayment starts a draft.
//
// Open to encoders: taking a payment at the counter is the everyday work this
// system exists for. The body is a payment.Draft, which carries no OR No. and
// no status — so a caller cannot supply either even by trying, because
// decodeJSON refuses unknown fields.
func handleCreatePayment(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		var draft payment.Draft
		if err := decodeJSON(r, &draft); err != nil {
			return Change{}, err
		}

		p, err := d.Payments.CreateTx(ctx, tx, draft, paymentActorOf(r))
		if err != nil {
			return Change{}, paymentError(err)
		}
		return Change{
			EntityID: paymentEntityID(p),
			After:    p,
			Status:   http.StatusCreated,
			Body:     p,
		}, nil
	}
}

func handleUpdatePayment(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := paymentID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		var draft payment.Draft
		if err := decodeJSON(r, &draft); err != nil {
			return Change{}, err
		}

		before, err := d.Payments.ByID(ctx, id)
		if err != nil {
			return Change{}, paymentError(err)
		}
		after, err := d.Payments.UpdateTx(ctx, tx, id, draft)
		if err != nil {
			return Change{}, paymentError(err)
		}
		return Change{EntityID: paymentEntityID(after), Before: before, After: after, Body: after}, nil
	}
}

// handleDeletePayment removes a draft. Safe for the reason the voucher's is: a
// draft moved no money, and what it said survives in the audit entry.
func handleDeletePayment(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := paymentID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		before, err := d.Payments.ByID(ctx, id)
		if err != nil {
			return Change{}, paymentError(err)
		}
		if err := d.Payments.DeleteTx(ctx, tx, id); err != nil {
			return Change{}, paymentError(err)
		}
		return Change{
			EntityID: paymentEntityID(before),
			Before:   before,
			Status:   http.StatusNoContent,
		}, nil
	}
}

// handlePostPayment turns a draft into money received.
//
// Open to encoders, because this is the counter's job — a farmer is standing
// there with cash. Undoing it is not: that is the void below, and it is a
// manager's.
func handlePostPayment(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := paymentID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		// Read before the transaction writes anything, on the pool's own
		// connection: PostTx needs its UPDATE to be the first statement *this
		// transaction* runs, and a read outside it holds no lock to upgrade.
		before, err := d.Payments.ByID(ctx, id)
		if err != nil {
			return Change{}, paymentError(err)
		}

		after, err := d.Payments.PostTx(ctx, tx, id, paymentActorOf(r))
		if err != nil {
			return Change{}, paymentError(err)
		}
		return Change{EntityID: paymentEntityID(after), Before: before, After: after, Body: after}, nil
	}
}

// handleVoidPayment reverses what a posted payment did.
//
// Manager-only: with voiding a voucher, this is one of the two ways money
// leaves the record.
func handleVoidPayment(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := paymentID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		var req voidRequest
		if err := decodeJSON(r, &req); err != nil {
			return Change{}, err
		}

		before, err := d.Payments.ByID(ctx, id)
		if err != nil {
			return Change{}, paymentError(err)
		}

		after, err := d.Payments.VoidTx(ctx, tx, id, paymentActorOf(r), req.Reason)
		if err != nil {
			return Change{}, paymentError(err)
		}
		return Change{EntityID: paymentEntityID(after), Before: before, After: after, Body: after}, nil
	}
}

// paymentActorOf is who is making the request. Every one of these routes is
// authenticated, so the session is always there; the zero Actor would be
// refused by the domain package anyway, which is the belt to this brace.
func paymentActorOf(r *http.Request) payment.Actor {
	user, ok := CurrentUser(r.Context())
	if !ok {
		return payment.Actor{}
	}
	return payment.Actor{UserID: user.ID, Username: user.Username}
}

// paymentEntityID is what the audit log files this event under: the id rather
// than the OR No., because a payment has to be traceable across every event in
// its life and it has no number for the first half of it.
func paymentEntityID(p payment.Payment) string { return strconv.FormatInt(p.ID, 10) }

func paymentID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("that is not a payment id")
	}
	return id, nil
}

// paymentError maps the domain's errors onto status codes. Anything it does not
// recognise is left alone, which the router turns into a 500 and a log line.
func paymentError(err error) error {
	if se, ok := asPaymentError(err); ok {
		return se
	}
	return err
}

func asPaymentError(err error) (statusError, bool) {
	var ve payment.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest("%s", ve.Message), true
	case errors.Is(err, payment.ErrNotFound):
		return statusError{http.StatusNotFound, "no such payment"}, true
	case errors.Is(err, payment.ErrNotDraft):
		// 409 rather than 400: nothing about the request is malformed, the
		// payment simply moved on — which is what a second click looks like.
		return conflict("%s", err.Error()), true
	case errors.Is(err, payment.ErrNotPosted), errors.Is(err, payment.ErrSettled):
		return conflict("%s", err.Error()), true
	default:
		return statusError{}, false
	}
}

func respondPaymentError(w http.ResponseWriter, r *http.Request, d Deps, err error, doing string) {
	if se, ok := asPaymentError(err); ok {
		writeJSON(w, se.status, errorBody{Error: se.message, RequestID: RequestID(r.Context())})
		return
	}
	d.Logger.Error(doing, "id", RequestID(r.Context()), "error", err)
	writeJSON(w, http.StatusInternalServerError, errorBody{
		Error: "internal error", RequestID: RequestID(r.Context())})
}
