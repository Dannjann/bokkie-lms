package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/Dannjann/bokkie-lms/internal/auth"
)

// handleListUsers lists the accounts. Manager-only: who has access is a
// management question, and an encoder does not need the roster.
func handleListUsers(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		users, err := d.Users.All(r.Context())
		if err != nil {
			d.Logger.Error("list users", "id", RequestID(r.Context()), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(r.Context())})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": users})
	}
}

type createUserRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Role        string `json:"role"`
}

// handleCreateUser adds an account.
//
// The audit entry records the new account's details but, obviously, not the
// password — Change.After is an auth.User, which has no hash field precisely so
// that this cannot happen by accident.
func handleCreateUser(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		var req createUserRequest
		if err := decodeJSON(r, &req); err != nil {
			return Change{}, err
		}

		user, err := auth.CreateUserTx(ctx, tx,
			normaliseUsername(req.Username), req.DisplayName, req.Password, auth.Role(req.Role))
		if err != nil {
			if se, ok := asStatusError(err); ok {
				return Change{}, se
			}
			return Change{}, err
		}

		return Change{
			EntityID: user.Username,
			After:    user,
			Status:   http.StatusCreated,
			Body:     user,
		}, nil
	}
}
