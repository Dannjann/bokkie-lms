package api

import (
	"context"
	"database/sql"
	"errors"
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

// The account transitions.
//
// All four go through rt.mutate, so the change and its audit entry commit
// together. None of them can leak a credential: every Before and After is an
// auth.User, and that struct has no hash field — the structural half of the
// guarantee handleCreateUser already relies on.

type setUserActiveRequest struct {
	Active bool `json:"active"`
}

// handleSetUserActive turns an account off or back on.
//
// Deactivation takes effect on the target's next request, because withSession
// re-reads the account every time and ends the session when it finds it
// inactive. Nothing here has to hunt for sessions.
func handleSetUserActive(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := pathID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		var req setUserActiveRequest
		if err := decodeJSON(r, &req); err != nil {
			return Change{}, err
		}

		before, err := auth.UserByIDTx(ctx, tx, id)
		if err != nil {
			return Change{}, asUserError(err)
		}
		if !req.Active {
			if err := refuseSelfChange(ctx, id,
				"you cannot deactivate your own account — another manager has to do it"); err != nil {
				return Change{}, err
			}
		}

		after, err := auth.SetActiveTx(ctx, tx, id, req.Active)
		if err != nil {
			return Change{}, asUserError(err)
		}
		if err := keepAManagerStanding(ctx, tx,
			"%s is the last manager who can still sign in — promote somebody else first",
			before.DisplayName); err != nil {
			return Change{}, err
		}

		action := "user.activated"
		if !req.Active {
			action = "user.deactivated"
		}
		return Change{
			Action:   action,
			EntityID: after.Username,
			Before:   before,
			After:    after,
			Body:     after,
		}, nil
	}
}

type setUserRoleRequest struct {
	Role string `json:"role"`
}

// handleSetUserRole promotes or demotes an account. Like deactivation it takes
// effect on the next request, because Role is re-read on that same path.
func handleSetUserRole(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := pathID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		var req setUserRoleRequest
		if err := decodeJSON(r, &req); err != nil {
			return Change{}, err
		}

		before, err := auth.UserByIDTx(ctx, tx, id)
		if err != nil {
			return Change{}, asUserError(err)
		}
		if auth.Role(req.Role) != auth.RoleManager {
			if err := refuseSelfChange(ctx, id,
				"you cannot take your own manager role away — another manager has to do it"); err != nil {
				return Change{}, err
			}
		}

		after, err := auth.SetRoleTx(ctx, tx, id, auth.Role(req.Role))
		if err != nil {
			return Change{}, asUserError(err)
		}
		if err := keepAManagerStanding(ctx, tx,
			"%s is the last manager — promote somebody else before taking this role away",
			before.DisplayName); err != nil {
			return Change{}, err
		}

		return Change{
			EntityID: after.Username,
			Before:   before,
			After:    after,
			Body:     after,
		}, nil
	}
}

type setPasswordRequest struct {
	Password string `json:"password"`
}

// handleResetUserPassword is a manager setting somebody else's password.
//
// It does not ask for the old one — that is the entire point of a reset, and
// the person asking for it is the one who has forgotten it. Every session the
// account holds is ended in the same transaction: the hash changing does not
// invalidate a session by itself, so an account whose password was just reset
// would otherwise stay signed in wherever it already was.
func handleResetUserPassword(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := pathID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		var req setPasswordRequest
		if err := decodeJSON(r, &req); err != nil {
			return Change{}, err
		}

		before, err := auth.UserByIDTx(ctx, tx, id)
		if err != nil {
			return Change{}, asUserError(err)
		}

		after, err := auth.SetPasswordTx(ctx, tx, id, req.Password)
		if err != nil {
			return Change{}, asUserError(err)
		}
		ended, err := auth.DeleteSessionsForUserTx(ctx, tx, id, "")
		if err != nil {
			return Change{}, err
		}

		// The entry records that the password changed and how many sessions
		// went with it. It records neither password: Before and After are the
		// user record, which cannot carry one.
		return Change{
			EntityID: after.Username,
			Before:   before,
			After:    after,
			Body:     map[string]any{"user": after, "sessions_ended": ended},
		}, nil
	}
}

type changeOwnPasswordRequest struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// handleChangeOwnPassword is anybody changing their own password.
//
// The current password is required. A manager resetting somebody else's does
// not need it — they are trusted with that by role — but a session is not
// proof of the password behind it, and without this check a browser left
// unlocked at a counter is enough to lock the owner out of their own account.
func handleChangeOwnPassword(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		user, ok := CurrentUser(ctx)
		if !ok {
			return Change{}, forbidden("please log in")
		}

		var req changeOwnPasswordRequest
		if err := decodeJSON(r, &req); err != nil {
			return Change{}, err
		}

		if err := auth.VerifyPasswordTx(ctx, tx, user.ID, req.Current); err != nil {
			if errors.Is(err, auth.ErrWrongPassword) || errors.Is(err, auth.ErrNoSuchUser) {
				// One message for both, as the login route does: which half
				// was wrong is not something to hand back.
				return Change{}, statusError{http.StatusUnauthorized,
					"that is not your current password"}
			}
			return Change{}, err
		}

		after, err := auth.SetPasswordTx(ctx, tx, user.ID, req.New)
		if err != nil {
			return Change{}, asUserError(err)
		}

		// Every other session, but not this one. The person is standing at this
		// tab and choosing a new password; signing them out of it reads as the
		// change having failed.
		spare := ""
		if sess, ok := CurrentSession(ctx); ok {
			spare = sess.ID
		}
		ended, err := auth.DeleteSessionsForUserTx(ctx, tx, user.ID, spare)
		if err != nil {
			return Change{}, err
		}

		return Change{
			EntityID: after.Username,
			Before:   user,
			After:    after,
			Body:     map[string]any{"user": after, "sessions_ended": ended},
		}, nil
	}
}

// refuseSelfChange stops an account doing to itself the two things that would
// take away both the screen and the fix in one click. It is the same argument
// that holds the LAN switch at the host machine.
func refuseSelfChange(ctx context.Context, targetID int64, message string) error {
	user, ok := CurrentUser(ctx)
	if ok && user.ID == targetID {
		return forbidden("%s", message)
	}
	return nil
}

// keepAManagerStanding is the last-manager guard, and it must be called *after*
// the write, inside the same transaction.
//
// Counting outside the transaction is what actually loses the station its last
// manager: the guard reads true on one connection while another connection is
// committing the write that makes it false. See
// TestTheManagerGuardHasToBeInsideTheTransaction, which loses both managers on
// every run with the count moved out.
// The refusal is a conflict rather than a bad request: what the caller sent was
// perfectly well formed, and would have been accepted a moment earlier or
// against a different account. It has to arrive as a sentence somebody can read,
// which is why it is a statusError and not a bare error that the wrapper would
// turn into "internal error" and a log line.
func keepAManagerStanding(ctx context.Context, tx *sql.Tx, format string, args ...any) error {
	count, err := auth.CountActiveManagersTx(ctx, tx)
	if err != nil {
		return err
	}
	if count == 0 {
		return conflict(format, args...)
	}
	return nil
}

// asUserError turns the auth package's errors into the status the caller sees.
func asUserError(err error) error {
	switch {
	case errors.Is(err, auth.ErrNoSuchUser):
		return statusError{http.StatusNotFound, "no such account"}
	case errors.Is(err, auth.ErrUsernameTaken):
		return conflict("%s", err.Error())
	}
	if se, ok := asStatusError(err); ok {
		return se
	}
	return err
}
