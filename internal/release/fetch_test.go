package release

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serve puts a signed release folder behind an HTTP server, the way a GitHub
// release serves its assets.
func serve(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(server.Close)
	return server
}

func TestFetchReturnsASignedManifest(t *testing.T) {
	dir, _ := signedRelease(t, "v1.2.3")
	server := serve(t, dir)

	manifest, err := Fetch(context.Background(), server.Client(), server.URL+"/")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if manifest.Version != "v1.2.3" {
		t.Errorf("version = %q, want v1.2.3", manifest.Version)
	}
}

// The whole point of fetching the signature rather than trusting the transport:
// an HTTPS connection to the wrong place is still an HTTPS connection.
func TestFetchRefusesAManifestSignedByAnotherKey(t *testing.T) {
	dir, _ := signedRelease(t, "v1.2.3")
	// A second release re-keys this build: the folder served below is still
	// internally consistent — its manifest and signature agree with each other
	// — but not with the key this binary now carries. That is exactly the shape
	// of a download from somewhere that is not us.
	signedRelease(t, "v9.9.9")

	server := serve(t, dir)
	_, err := Fetch(context.Background(), server.Client(), server.URL+"/")
	if err == nil {
		t.Fatal("a manifest signed by another key was accepted")
	}
	if !strings.Contains(err.Error(), "not signed by the bokkie release key") {
		t.Errorf("error = %v, want it to say the signature is not ours", err)
	}
}

func TestFetchRefusesWhenTheSignatureIsMissing(t *testing.T) {
	dir, _ := signedRelease(t, "v1.2.3")
	if err := os.Remove(filepath.Join(dir, SignatureName)); err != nil {
		t.Fatal(err)
	}

	server := serve(t, dir)
	_, err := Fetch(context.Background(), server.Client(), server.URL+"/")
	if err == nil {
		t.Fatal("a release with no signature beside it was accepted")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want it to name what the server answered", err)
	}
}

// Review Focus 3. A station behind a captive portal must fall into the offline
// path, not stand there with the window open.
func TestFetchGivesUpRatherThanHanging(t *testing.T) {
	blocked := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	// Registered in this order on purpose: cleanups run last-registered-first,
	// and server.Close waits for the handler, so the handler has to be released
	// before Close is reached or the test deadlocks instead of failing.
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(blocked) })

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Fetch(ctx, server.Client(), server.URL+"/")
	if err == nil {
		t.Fatal("a server that never answers was treated as a genuine release")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want the deadline", err)
	}
	if elapsed := time.Since(start); elapsed > FetchTimeout {
		t.Errorf("waited %s; the caller's own deadline must win over FetchTimeout", elapsed)
	}
}
