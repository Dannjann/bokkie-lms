package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/app"
	"github.com/Dannjann/bokkie-lms/internal/config"
)

// newServeTestApp builds an app on a scratch data directory and a port the
// test knows.
//
// A concrete port rather than 0: this test has to re-bind the port after
// shutdown to prove it was released, and the app does not hand its listener
// back out.
func newServeTestApp(t *testing.T) (*app.App, int) {
	t.Helper()

	port := freePort(t)
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Port = port
	if err := config.EnsureDirs(cfg); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	server, err := app.New(context.Background(), cfg, testLogger(t), app.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return server, port
}

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// freePort asks the OS for a port and gives it straight back. There is a race
// between releasing it and the app taking it, and it is the same race every Go
// test that needs a known port runs; nothing else on a test machine is hunting
// for this one.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitUntilServing(t *testing.T, port int) {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	client := &http.Client{Timeout: time.Second}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the app never answered on %s", url)
}

func TestServeUntilStopsWhenToldTo(t *testing.T) {
	server, port := newServeTestApp(t)
	stop := make(chan struct{})

	done := make(chan error, 1)
	go func() { done <- serveUntil(context.Background(), testLogger(t), server, stop) }()

	waitUntilServing(t, port)
	close(stop)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveUntil: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("serveUntil did not return after it was told to stop; the window would not close")
	}
}

// The port has to be free afterwards, or the next time somebody opens the
// window it will not start and the reason will be invisible.
func TestServeUntilReleasesThePort(t *testing.T) {
	server, port := newServeTestApp(t)
	stop := make(chan struct{})

	done := make(chan error, 1)
	go func() { done <- serveUntil(context.Background(), testLogger(t), server, stop) }()
	waitUntilServing(t, port)

	close(stop)
	if err := <-done; err != nil {
		t.Fatalf("serveUntil: %v", err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		t.Fatalf("the port was still held after shutdown: %v", err)
	}
	listener.Close()
}
