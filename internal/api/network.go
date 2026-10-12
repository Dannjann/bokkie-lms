package api

// Network access control: whether a request that came from somewhere other
// than the host machine is served at all.
//
// # What this is, and what it is not
//
// It is a policy control. The listener stays on 0.0.0.0 either way, so with LAN
// access off the port is still open and a machine on the station network still
// completes a TCP connection — it just gets a refusal instead of the app. That
// is enough for what it is for: "the counter PCs should not be in here today"
// is a decision about who is allowed to work, not a defence against an attacker
// on the wire.
//
// It is not a security boundary. A source address on a LAN with no TLS is a
// claim, not a proof, and nothing here would stop someone who could set their
// own. The architecture already says this (docs/plans/00-architecture.md,
// "Security"): plain HTTP on a private LAN, with the login and the audit log
// doing the work that actually matters. This switch closes the door; it does
// not lock it, and the UI copy says so rather than implying otherwise.

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// hostAddressTTL is how long the host's own addresses are believed before they
// are looked up again. DHCP can move this machine's address, and a cache that
// never expired would eventually refuse the host itself — the one machine that
// must never be locked out.
const hostAddressTTL = 30 * time.Second

// withNetworkGate refuses requests from anywhere but the host when LAN access
// is switched off.
//
// It sits outside the session middleware: a machine that is not allowed in has
// no business having its cookie read, and resolving a session for a request
// that is about to be refused is work done for nobody.
func withNetworkGate(d Deps, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The common case is LAN access on, and it costs one cached map read.
		// Nothing below runs on a normal station day.
		if d.Settings.Bool(settings.LANAccess) {
			next.ServeHTTP(w, r)
			return
		}
		if isHostRequest(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Logged, not audited. There is no user to attribute this to — the
		// session has deliberately not been resolved yet — and a counter PC
		// left open on the dashboard re-polls every few seconds, so auditing
		// each refusal would bury the entries that matter under one machine
		// nobody switched off. The request log already has every line.
		d.Logger.Info("refused a request from outside the host",
			"id", RequestID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"remote", clientIP(r),
		)
		refuseNonHost(w, r)
	})
}

// refuseNonHost answers a blocked request in whatever form the caller can read:
// JSON for the API, a page for a browser. A browser that asked for the app and
// got a bare JSON error would show a line of punctuation to someone who needs a
// sentence explaining why the screen they use all day has gone.
func refuseNonHost(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusForbidden, errorBody{
			Error:     "this station is set to allow the host machine only",
			RequestID: RequestID(r.Context()),
		})
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write(hostOnlyPage)
}

// hostOnlyPage is deliberately a self-contained handful of HTML with no assets:
// it has to render on a machine that is being refused the app bundle, which
// rules out anything the SPA would have loaded.
var hostOnlyPage = []byte(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Bokkie — host machine only</title>
<style>
  body { margin: 0; min-height: 100vh; display: grid; place-items: center;
         font: 16px/1.55 system-ui, -apple-system, "Segoe UI", sans-serif;
         color: #1f2937; background: #f8fafc; padding: 1.5rem; }
  main { max-width: 30rem; background: #fff; border: 1px solid #e2e8f0;
         border-radius: 8px; padding: 1.75rem 2rem; }
  h1 { margin: 0 0 0.75rem; font-size: 1.125rem; }
  p { margin: 0 0 0.75rem; }
  p:last-child { margin-bottom: 0; color: #64748b; font-size: 0.875rem; }
</style>
<main>
  <h1>This station is set to the host machine only</h1>
  <p>Bokkie is running, but it is currently set to refuse every machine except
     the one it is installed on. Nothing is broken and no data is affected.</p>
  <p>A manager can switch network access back on from the dashboard, at the host
     machine itself.</p>
</main>
</html>
`)

// isHostRequest reports whether a request came from the machine bokkie runs on.
//
// Loopback is the obvious half. The other half is the host's own LAN address:
// someone at the host who opens http://bokkie.local:8080 rather than localhost
// arrives from a routable address, and locking that person out of the switch
// they are standing in front of would be an absurd way to fail.
func isHostRequest(r *http.Request) bool {
	ip := net.ParseIP(clientIP(r))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	return hostAddresses.has(ip)
}

// hostAddresses is shared rather than held per-handler because it describes the
// machine, not any one request path — the gate and the session endpoint are
// asking the same question and should never disagree about the answer.
var hostAddresses = &addressCache{}

type addressCache struct {
	mu        sync.Mutex
	addresses map[string]struct{}
	read      time.Time
}

func (c *addressCache) has(ip net.IP) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.addresses == nil || time.Since(c.read) > hostAddressTTL {
		c.addresses, c.read = localAddresses(), time.Now()
	}
	_, ok := c.addresses[ip.String()]
	return ok
}

// localAddresses is every address configured on this machine.
//
// Unlike discovery's list this keeps link-local and IPv6 addresses. That list
// answers "which address should the other machines be sent to", where a
// link-local one would be useless; this one answers "is this us", where every
// address we hold counts. An interface that cannot be read is skipped rather
// than failing the lookup — one unreadable adapter must not lock out the host.
func localAddresses() map[string]struct{} {
	found := make(map[string]struct{})

	interfaces, err := net.Interfaces()
	if err != nil {
		return found
	}
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				found[ipNet.IP.String()] = struct{}{}
			}
		}
	}
	return found
}
