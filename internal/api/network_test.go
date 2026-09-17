package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/auth"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// hostAddr and lanAddr stand for the two machines this whole feature is about.
// httptest.NewRequest already defaults to a TEST-NET address, which is exactly
// the "some other machine on the network" case, so the tests below only have to
// be explicit about the host.
const (
	hostAddr = "127.0.0.1:51000"
	lanAddr  = "192.168.7.42:51000"
)

func TestLANAccessOffServesTheHostAndRefusesEveryoneElse(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	setLANAccess(t, deps, false)

	t.Run("the host still gets the app", func(t *testing.T) {
		resp := requestFrom(t, handler, http.MethodGet, "/api/session", hostAddr, nil, nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 — the host must never be locked out", resp.Code)
		}
	})

	t.Run("another machine gets a page it can read", func(t *testing.T) {
		resp := requestFrom(t, handler, http.MethodGet, "/customers", lanAddr, nil, nil)

		if resp.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", resp.Code)
		}
		if ct := resp.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("content type = %q, want HTML — a browser needs a sentence, not a JSON blob", ct)
		}
		if !strings.Contains(resp.Body.String(), "host machine only") {
			t.Errorf("body = %q, want it to say why the screen has gone", resp.Body)
		}
	})

	t.Run("the API still answers as JSON", func(t *testing.T) {
		resp := requestFrom(t, handler, http.MethodGet, "/api/customers", lanAddr, nil, nil)

		if resp.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", resp.Code)
		}
		if ct := resp.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("content type = %q, want JSON — an API that answers HTML is baffling to debug", ct)
		}
	})

	// The gate is outside the session middleware, so a refused machine is
	// refused whether or not it holds a perfectly good cookie. Access is about
	// where the request came from, and a valid login does not move a PC.
	t.Run("a valid session does not get a machine in", func(t *testing.T) {
		setLANAccess(t, deps, true)
		seedUser(t, deps, "elena", "correct-horse-battery", auth.RoleManager)
		cookies := login(t, handler, "elena", "correct-horse-battery")
		setLANAccess(t, deps, false)

		resp := requestFrom(t, handler, http.MethodGet, "/api/customers", lanAddr, cookies, nil)
		if resp.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403 even with a live session", resp.Code)
		}
	})
}

func TestLANAccessOnServesEveryMachine(t *testing.T) {
	handler := NewHandler(testDeps(t, openTestDB(t)))

	// The default, asserted rather than assumed: an install nobody has
	// configured has to behave as it always did.
	for _, addr := range []string{hostAddr, lanAddr} {
		resp := requestFrom(t, handler, http.MethodGet, "/api/session", addr, nil, nil)
		if resp.Code != http.StatusOK {
			t.Errorf("status from %s = %d, want 200", addr, resp.Code)
		}
	}
}

// The lockout this guards against is the one that cannot be undone from the
// screen that caused it: a manager at a counter PC switching the network off
// loses the app mid-click, along with the switch that would put it back.
func TestNetworkSwitchCanOnlyBeChangedAtTheHost(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	handler := NewHandler(deps)
	seedUser(t, deps, "elena", "correct-horse-battery", auth.RoleManager)
	cookies := login(t, handler, "elena", "correct-horse-battery")

	t.Run("refused from a counter PC", func(t *testing.T) {
		resp := requestFrom(t, handler, http.MethodPut, "/api/settings", lanAddr, cookies,
			updateSettingRequest{Key: settings.LANAccess.Key, Value: "false"})

		if resp.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.Code)
		}
		if !strings.Contains(resp.Body.String(), "host machine") {
			t.Errorf("body = %q, want it to name where the switch lives", resp.Body)
		}
		if got := deps.Settings.Bool(settings.LANAccess); !got {
			t.Error("the setting changed anyway; the refusal has to be the whole story")
		}
	})

	t.Run("allowed at the host", func(t *testing.T) {
		resp := requestFrom(t, handler, http.MethodPut, "/api/settings", hostAddr, cookies,
			updateSettingRequest{Key: settings.LANAccess.Key, Value: "false"})

		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body)
		}
		if deps.Settings.Bool(settings.LANAccess) {
			t.Error("the setting did not take effect; it must not need a restart")
		}
	})

	// Every other setting is unaffected — the host-only rule belongs to this
	// one key, not to the settings screen as a whole.
	t.Run("other settings are still changeable from anywhere", func(t *testing.T) {
		// The subtest above left the network switched off, which would have the
		// gate refuse this request long before the settings handler saw it.
		setLANAccess(t, deps, true)

		resp := requestFrom(t, handler, http.MethodPut, "/api/settings", lanAddr, cookies,
			updateSettingRequest{Key: settings.BackupRetention.Key, Value: "21"})
		if resp.Code != http.StatusOK {
			t.Errorf("status = %d, want 200: %s", resp.Code, resp.Body)
		}
	})
}

// Turning access off has to take the LAN name down with it. A phone that
// resolves bokkie.local and is then refused looks like a fault; a name that
// does not resolve looks like what it is.
func TestNetworkSwitchMovesTheLANName(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	publisher := &fakeDiscovery{}
	deps.Discovery = publisher

	handler := NewHandler(deps)
	seedUser(t, deps, "elena", "correct-horse-battery", auth.RoleManager)
	cookies := login(t, handler, "elena", "correct-horse-battery")

	put := func(key, value string) {
		t.Helper()
		resp := requestFrom(t, handler, http.MethodPut, "/api/settings", hostAddr, cookies,
			updateSettingRequest{Key: key, Value: value})
		if resp.Code != http.StatusOK {
			t.Fatalf("PUT %s=%s: status %d: %s", key, value, resp.Code, resp.Body)
		}
	}

	put(settings.LANAccess.Key, "false")
	put(settings.LANAccess.Key, "true")
	put(settings.BackupRetention.Key, "21") // an unrelated setting must not touch the name

	if got := publisher.calls(); len(got) != 2 || got[0] || !got[1] {
		t.Errorf("SetPublished calls = %v, want exactly [false true]", got)
	}
}

// A settings change must not depend on a working mDNS responder: an install
// with mdns.enabled off in config.yaml has no publisher at all.
func TestNetworkSwitchWorksWithoutADiscoveryPublisher(t *testing.T) {
	deps := testDeps(t, openTestDB(t))
	deps.Discovery = nil

	handler := NewHandler(deps)
	seedUser(t, deps, "elena", "correct-horse-battery", auth.RoleManager)
	cookies := login(t, handler, "elena", "correct-horse-battery")

	resp := requestFrom(t, handler, http.MethodPut, "/api/settings", hostAddr, cookies,
		updateSettingRequest{Key: settings.LANAccess.Key, Value: "false"})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body)
	}
}

// The dashboard hides the switch on machines that are not the host, so the
// session answer has to say which kind of machine is asking.
func TestSessionSaysWhetherThisIsTheHost(t *testing.T) {
	handler := NewHandler(testDeps(t, openTestDB(t)))

	for addr, want := range map[string]bool{hostAddr: true, lanAddr: false} {
		resp := requestFrom(t, handler, http.MethodGet, "/api/session", addr, nil, nil)
		var body sessionResponse
		decode(t, resp, &body)
		if body.Host != want {
			t.Errorf("host from %s = %v, want %v", addr, body.Host, want)
		}
	}
}

// --- helpers ---------------------------------------------------------------

func setLANAccess(t *testing.T, deps Deps, on bool) {
	t.Helper()
	value := "false"
	if on {
		value = "true"
	}
	if err := deps.Settings.Set(context.Background(), settings.LANAccess, value); err != nil {
		t.Fatalf("set %s=%s: %v", settings.LANAccess.Key, value, err)
	}
}

// requestFrom is request with a say in which machine the request came from,
// which is the only variable these tests are actually about.
func requestFrom(t *testing.T, h http.Handler, method, path, remote string,
	cookies []*http.Cookie, body any,
) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	req := buildRequest(t, method, path, cookies, body)
	req.RemoteAddr = remote
	h.ServeHTTP(rec, req)
	return rec
}

type fakeDiscovery struct {
	mu        sync.Mutex
	published []bool
}

func (f *fakeDiscovery) SetPublished(published bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, published)
	return nil
}

func (f *fakeDiscovery) calls() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.published...)
}
