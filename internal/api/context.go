package api

import (
	"context"
	"net"
	"net/http"

	"github.com/Dannjann/bokkie-lms/internal/auth"
)

const (
	userKey contextKey = iota + 1
	sessionKey
)

// withUser attaches the authenticated user and their session to the request.
func withUser(ctx context.Context, u auth.User, s auth.Session) context.Context {
	return context.WithValue(context.WithValue(ctx, userKey, u), sessionKey, s)
}

// CurrentUser returns the authenticated user, and false if the request is
// anonymous. Handlers behind requireRole can rely on ok being true; the ones in
// front of it — login, setup — cannot.
func CurrentUser(ctx context.Context) (auth.User, bool) {
	u, ok := ctx.Value(userKey).(auth.User)
	return u, ok
}

// CurrentSession returns the session backing the request.
func CurrentSession(ctx context.Context) (auth.Session, bool) {
	s, ok := ctx.Value(sessionKey).(auth.Session)
	return s, ok
}

// clientIP is the address recorded in the audit log.
//
// It reads RemoteAddr only, never X-Forwarded-For. There is no proxy in front
// of bokkie — it is the station's own binary on the station's own LAN — so a
// forwarded header here could only have been set by the client, and trusting it
// would let anyone write whatever address they liked into a financial record.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
