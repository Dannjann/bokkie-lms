package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/config"
)

// The gate for this task is "shutdown drains in-flight requests rather than
// cutting them off", so that is what this exercises: a request that is still
// being served when Stop is called must receive its full response.
func TestStopDrainsInFlightRequests(t *testing.T) {
	server := newTestApp(t)
	if err := server.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	responses := make(chan *http.Response, 1)
	go func() {
		resp, err := http.Get("http://" + server.Addr().String() + "/healthz")
		if err != nil {
			t.Errorf("GET /healthz: %v", err)
			close(responses)
			return
		}
		responses <- resp
	}()

	// Give the request time to be in flight, then shut down under it.
	time.Sleep(50 * time.Millisecond)
	if err := server.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	resp, ok := <-responses
	if !ok {
		t.Fatal("request failed during shutdown")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 — the request was cut off", resp.StatusCode)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Errorf("read body: %v", err)
	}
}

func TestRunReturnsWhenContextIsCancelled(t *testing.T) {
	server := newTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel() // stands in for SIGINT, SIGTERM, or a Windows service stop

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

// newTestApp builds an app on a scratch data directory and an ephemeral port.
func newTestApp(t *testing.T) *App {
	t.Helper()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Port = 0 // let the OS pick, so parallel tests cannot collide
	if err := config.EnsureDirs(cfg); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	server, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return server
}
