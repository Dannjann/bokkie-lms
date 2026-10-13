package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeStation stands in for the running app.
type fakeStation struct {
	stopped bool
}

func (f *fakeStation) LastBackup() (time.Time, string) { return time.Time{}, "" }
func (f *fakeStation) Port() int                       { return 8080 }
func (f *fakeStation) Hostname() string                { return "bokkie" }
func (f *fakeStation) RequestStop()                    { f.stopped = true }

// controlHandler wraps the control layer around a handler that fails the test
// if it is ever reached for a control path.
func controlHandler(t *testing.T, station Station) (http.Handler, *fakeStation) {
	t.Helper()
	fake, _ := station.(*fakeStation)

	passthrough := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // "the control layer let this through"
	})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return withControl(Deps{Logger: log, Station: station}, passthrough), fake
}

// Review Focus 2. The LAN switch being on is a decision about who may work; it
// is not a decision about who may switch the station off.
func TestStopIsRefusedFromTheStationNetwork(t *testing.T) {
	handler, station := controlHandler(t, &fakeStation{})

	req := httptest.NewRequest(http.MethodPost, "/api/control/stop", nil)
	req.RemoteAddr = "192.168.1.77:51000" // a counter PC
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("a counter PC got %d from the stop button; it must be refused", rec.Code)
	}
	if station.stopped {
		t.Fatal("the station was stopped by a machine that is not the host")
	}
}

func TestStopIsAcceptedFromTheHost(t *testing.T) {
	handler, station := controlHandler(t, &fakeStation{})

	req := httptest.NewRequest(http.MethodPost, "/api/control/stop", nil)
	req.RemoteAddr = "127.0.0.1:51000"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("the host got %d from the stop button", rec.Code)
	}
	// RequestStop runs in a goroutine so the response lands first.
	deadline := time.Now().Add(2 * time.Second)
	for !station.stopped && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !station.stopped {
		t.Error("the host pressed stop and the station was never asked to stop")
	}
}

// Status is as host-only as Stop. It names the machine's addresses and its
// backup history, which is nobody's business from a counter PC.
func TestStatusIsAlsoHostOnly(t *testing.T) {
	handler, _ := controlHandler(t, &fakeStation{})

	req := httptest.NewRequest(http.MethodGet, "/api/control/status", nil)
	req.RemoteAddr = "192.168.1.77:51000"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("a counter PC got %d from status; it must be refused", rec.Code)
	}
}

// Everything that is not a control path must pass straight through, or the
// control layer would have swallowed the lending API.
func TestNonControlPathsPassThrough(t *testing.T) {
	handler, _ := controlHandler(t, &fakeStation{})

	req := httptest.NewRequest(http.MethodGet, "/api/customers", nil)
	req.RemoteAddr = "192.168.1.77:51000"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Errorf("a non-control path got %d; the control layer did not pass it on", rec.Code)
	}
}
