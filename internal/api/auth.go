package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/audit"
	"github.com/Dannjann/bokkie-lms/internal/auth"
)

// withSession resolves the session cookie into a user on the request context.
//
// It never rejects anything. Deciding what a request is allowed to do is the
// route guard's job — this middleware only answers "who is this", so that the
// login page and the API can sit behind the same chain.
func withSession(d Deps, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.CookieName)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		ctx := r.Context()
		sess, err := d.Sessions.Lookup(ctx, cookie.Value)
		if err != nil {
			// Expired or unknown. Clear the cookie so the browser stops
			// sending a dead token on every request for the next month.
			http.SetCookie(w, auth.ClearCookie())
			next.ServeHTTP(w, r)
			return
		}

		user, err := d.Users.ByID(ctx, sess.UserID)
		if err != nil || !user.Active {
			// The account was deleted or deactivated while the session lived.
			// Ending the session here is what makes deactivation take effect
			// immediately rather than in thirty days.
			_ = d.Sessions.Delete(ctx, sess.ID)
			http.SetCookie(w, auth.ClearCookie())
			next.ServeHTTP(w, r)
			return
		}

		d.Sessions.Touch(ctx, sess)
		next.ServeHTTP(w, r.WithContext(withUser(ctx, user, sess)))
	})
}

// sessionResponse is what the SPA needs to decide which screen to show: who is
// logged in, or that nobody is, or that this install has no accounts at all and
// belongs on the first-run page.
type sessionResponse struct {
	NeedsSetup bool       `json:"needs_setup"`
	User       *auth.User `json:"user,omitempty"`

	// LockedUser is the username a Lock action left behind, so the login form
	// can prefill it and the person only has to type a password.
	LockedUser string `json:"locked_user,omitempty"`

	// Host says this browser is on the machine bokkie runs on. The dashboard
	// needs it to know whether the network switch is its to touch: that switch
	// is host-only, and a control that refuses when pressed is worse than one
	// that says up front whose it is.
	Host bool `json:"host"`
}

// writeSession is the one way a session answer leaves this package.
//
// Four handlers answer with a sessionResponse, and Host has to be right in all
// of them: the dashboard reads it after a login just as it does on a page load,
// and a field that three handlers set and the fourth forgets would show the
// switch as somebody else's on the very machine that owns it. Stamping it here
// means no handler has to remember.
func writeSession(w http.ResponseWriter, r *http.Request, status int, resp sessionResponse) {
	resp.Host = isHostRequest(r)
	writeJSON(w, status, resp)
}

// handleSession answers "who am I".
//
// This is the SPA's first call on every load, and it is deliberately public:
// answering 401 here would make the login page itself impossible to render
// without a special case.
func handleSession(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		needsSetup, err := d.Users.NeedsSetup(r.Context())
		if err != nil {
			d.Logger.Error("count users", "id", RequestID(r.Context()), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(r.Context())})
			return
		}

		resp := sessionResponse{NeedsSetup: needsSetup}
		if user, ok := CurrentUser(r.Context()); ok {
			resp.User = &user
		}
		if c, err := r.Cookie(lockedUserCookie); err == nil {
			resp.LockedUser = c.Value
		}
		writeSession(w, r, http.StatusOK, resp)
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleLogin authenticates and starts a session.
//
// It writes its own audit entries rather than using the route wrapper, because
// the interesting entries here are the *failures*: a wrapper that only records
// what succeeded would miss every event this endpoint exists to leave a trace
// of.
func handleLogin(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		var req loginRequest
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: err.Error(), RequestID: RequestID(ctx)})
			return
		}
		username := normaliseUsername(req.Username)

		if locked, remaining := d.Limiter.Locked(username); locked {
			d.Audit.RecordOrLog(ctx, audit.Entry{
				Username:  username,
				Action:    audit.ActionLoginBlocked,
				RequestID: RequestID(ctx),
				IP:        clientIP(r),
			})
			writeJSON(w, http.StatusTooManyRequests, errorBody{
				Error:     "too many failed attempts — try again in " + humanDuration(remaining),
				RequestID: RequestID(ctx),
			})
			return
		}

		user, err := d.Users.Authenticate(ctx, username, req.Password)
		if err != nil {
			d.Limiter.Fail(username)

			// The audit entry says which it was; the response never does.
			// Telling an unauthenticated caller that a username exists hands
			// them the first half of the answer.
			d.Audit.RecordOrLog(ctx, audit.Entry{
				UserID:    user.ID,
				Username:  username,
				Action:    audit.ActionLoginFailed,
				After:     map[string]string{"reason": loginFailureReason(err)},
				RequestID: RequestID(ctx),
				IP:        clientIP(r),
			})

			if !errors.Is(err, auth.ErrNoSuchUser) && !errors.Is(err, auth.ErrWrongPassword) &&
				!errors.Is(err, auth.ErrUserInactive) {
				d.Logger.Error("authenticate", "id", RequestID(ctx), "error", err)
			}

			writeJSON(w, http.StatusUnauthorized, errorBody{
				Error:     "username or password is incorrect",
				RequestID: RequestID(ctx),
			})
			return
		}

		d.Limiter.Succeed(username)
		if err := startSession(ctx, w, r, d, user); err != nil {
			d.Logger.Error("start session", "id", RequestID(ctx), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}

		d.Audit.RecordOrLog(ctx, audit.Entry{
			UserID:    user.ID,
			Username:  user.Username,
			Action:    audit.ActionLoginSucceeded,
			RequestID: RequestID(ctx),
			IP:        clientIP(r),
		})

		if err := d.Users.MarkLoggedIn(ctx, user.ID, time.Now()); err != nil {
			// The session is already valid; this column is a convenience.
			d.Logger.Warn("could not record last login", "id", RequestID(ctx), "error", err)
		}

		writeSession(w, r, http.StatusOK, sessionResponse{User: &user})
	}
}

func startSession(ctx context.Context, w http.ResponseWriter, r *http.Request, d Deps, user auth.User) error {
	token, sess, err := d.Sessions.Create(ctx, user.ID, clientIP(r))
	if err != nil {
		return err
	}
	http.SetCookie(w, auth.NewCookie(token, sess.ExpiresAt))
	http.SetCookie(w, clearLockedUser())
	return nil
}

// lockedUserCookie remembers who locked the screen, so the login form can
// prefill the username. It holds no authority whatsoever — worst case someone
// sees a name that is already written on the roster by the door.
const lockedUserCookie = "bokkie_locked_user"

func setLockedUser(username string) *http.Cookie {
	return &http.Cookie{
		Name:     lockedUserCookie,
		Value:    username,
		Path:     "/",
		MaxAge:   int((30 * 24 * time.Hour).Seconds()),
		HttpOnly: false, // read by the login form; there is nothing to protect
		SameSite: http.SameSiteLaxMode,
	}
}

func clearLockedUser() *http.Cookie {
	return &http.Cookie{Name: lockedUserCookie, Value: "", Path: "/", MaxAge: -1, SameSite: http.SameSiteLaxMode}
}

// handleLogout ends the session and forgets who was here.
func handleLogout(d Deps) http.HandlerFunc {
	return endSession(d, audit.ActionLogout, false)
}

// handleLock ends the session but remembers the username.
//
// Lock and logout destroy the session identically — the difference is entirely
// what the next person sees. Lock says "I'm stepping away, I'll be back", so
// the login form comes up with the name filled in and only a password to type.
// That difference is what makes a long session tolerable without an idle
// timeout: locking is a one-click habit rather than a chore.
func handleLock(d Deps) http.HandlerFunc {
	return endSession(d, audit.ActionLock, true)
}

func endSession(d Deps, action string, remember bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		user, hasUser := CurrentUser(ctx)
		if sess, ok := CurrentSession(ctx); ok {
			if err := d.Sessions.Delete(ctx, sess.ID); err != nil {
				d.Logger.Warn("could not delete session", "id", RequestID(ctx), "error", err)
			}
		}

		http.SetCookie(w, auth.ClearCookie())
		if remember && hasUser {
			http.SetCookie(w, setLockedUser(user.Username))
		} else {
			http.SetCookie(w, clearLockedUser())
		}

		if hasUser {
			d.Audit.RecordOrLog(ctx, audit.Entry{
				UserID:    user.ID,
				Username:  user.Username,
				Action:    action,
				RequestID: RequestID(ctx),
				IP:        clientIP(r),
			})
		}

		resp := sessionResponse{}
		if remember && hasUser {
			resp.LockedUser = user.Username
		}
		writeSession(w, r, http.StatusOK, resp)
	}
}

type setupRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

// handleSetup creates the first administrator, once and never again.
//
// It is public by necessity — there is nobody to authenticate as yet — and
// self-audited because it both creates a user and starts a session, and because
// its refusal (setup already done) is itself worth recording: a POST to this
// endpoint on an established install is either a confused operator or someone
// trying their luck.
func handleSetup(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		var req setupRequest
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error(), RequestID: RequestID(ctx)})
			return
		}

		user, err := d.Users.CreateFirstAdmin(ctx,
			normaliseUsername(req.Username), req.DisplayName, req.Password)
		switch {
		case errors.Is(err, auth.ErrAlreadySetUp):
			d.Audit.RecordOrLog(ctx, audit.Entry{
				Username:  normaliseUsername(req.Username),
				Action:    audit.ActionSetupRefused,
				Entity:    "setup",
				After:     map[string]string{"reason": "setup has already been completed"},
				RequestID: RequestID(ctx),
				IP:        clientIP(r),
			})
			writeJSON(w, http.StatusConflict, errorBody{
				Error:     "this system has already been set up — log in instead",
				RequestID: RequestID(ctx),
			})
			return
		case errors.Is(err, auth.ErrUsernameTaken):
			writeJSON(w, http.StatusConflict, errorBody{Error: err.Error(), RequestID: RequestID(ctx)})
			return
		case err != nil:
			if se, ok := asStatusError(err); ok {
				writeJSON(w, se.status, errorBody{Error: se.message, RequestID: RequestID(ctx)})
				return
			}
			d.Logger.Error("create first admin", "id", RequestID(ctx), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}

		if err := startSession(ctx, w, r, d, user); err != nil {
			d.Logger.Error("start session", "id", RequestID(ctx), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}

		d.Audit.RecordOrLog(ctx, audit.Entry{
			UserID:    user.ID,
			Username:  user.Username,
			Action:    audit.ActionSetupCompleted,
			Entity:    "user",
			EntityID:  user.Username,
			After:     user,
			RequestID: RequestID(ctx),
			IP:        clientIP(r),
		})
		d.Logger.Info("first administrator created", "username", user.Username)

		writeSession(w, r, http.StatusCreated, sessionResponse{User: &user})
	}
}

// loginFailureReason is what goes in the audit entry — the detail the response
// withholds. Reading the audit log months later, "wrong password" and "no such
// user" tell very different stories about the same forty attempts.
func loginFailureReason(err error) string {
	switch {
	case errors.Is(err, auth.ErrNoSuchUser):
		return "no such user"
	case errors.Is(err, auth.ErrWrongPassword):
		return "wrong password"
	case errors.Is(err, auth.ErrUserInactive):
		return "account is deactivated"
	default:
		return "error: " + err.Error()
	}
}

// asStatusError maps an auth package error onto a response. Validation
// complaints are shown verbatim — they were written for the person at the
// counter — and anything unrecognised stays a fault.
func asStatusError(err error) (statusError, bool) {
	var ve auth.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest("%s", ve.Message), true
	case errors.Is(err, auth.ErrUsernameTaken):
		return conflict("%s", err.Error()), true
	default:
		return statusError{}, false
	}
}

func normaliseUsername(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// humanDuration renders a lockout as something worth reading on a login form:
// "try again in 12 minutes" is actionable where "too many attempts" leaves
// someone retrying pointlessly.
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}
	minutes := int(d.Round(time.Minute).Minutes())
	if minutes == 1 {
		return "1 minute"
	}
	return strconv.Itoa(minutes) + " minutes"
}
