package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
)

// Barangays: the list a manager keeps in Settings, and the picker on the
// customer form. Reading is open to everyone who can add a customer; changing
// the list is a manager's, because a prefix decides every code issued under it.

func handleListBarangays(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := d.Customers.Barangays(r.Context())
		if err != nil {
			d.Logger.Error("list barangays", "id", RequestID(r.Context()), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(r.Context())})
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

func handleCreateBarangay(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		var draft customer.BarangayDraft
		if err := decodeJSON(r, &draft); err != nil {
			return Change{}, err
		}
		b, err := d.Customers.CreateBarangayTx(ctx, tx, draft)
		if err != nil {
			return Change{}, barangayError(err)
		}
		return Change{EntityID: b.Prefix, After: b, Status: http.StatusCreated, Body: b}, nil
	}
}

func handleUpdateBarangay(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := pathID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}
		var draft customer.BarangayDraft
		if err := decodeJSON(r, &draft); err != nil {
			return Change{}, err
		}
		before, err := d.Customers.BarangayByID(ctx, id)
		if err != nil {
			return Change{}, barangayError(err)
		}
		after, err := d.Customers.UpdateBarangayTx(ctx, tx, id, draft)
		if err != nil {
			return Change{}, barangayError(err)
		}
		return Change{EntityID: strconv.FormatInt(after.ID, 10), Before: before, After: after, Body: after}, nil
	}
}

func barangayError(err error) error {
	var ve customer.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest("%s", ve.Message)
	case errors.Is(err, customer.ErrBarangayNotFound):
		return statusError{http.StatusNotFound, "no such barangay"}
	case errors.Is(err, customer.ErrPrefixInUse):
		return conflict("%s", err.Error())
	}
	return err
}
