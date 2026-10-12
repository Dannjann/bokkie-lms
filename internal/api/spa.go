package api

import (
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
)

// newSPAHandler serves the embedded single-page app.
//
// Unknown paths return index.html so that deep links and browser refreshes work
// — the router in the SPA decides what the path means. Only /api and /healthz
// are exempt, and they are matched more specifically by the mux, so they never
// reach this handler.
func newSPAHandler(spa fs.FS) http.Handler {
	files := http.FileServerFS(spa)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")

		if name == "" || !fileExists(spa, name) {
			serveIndex(spa, w, r)
			return
		}

		// Asset filenames carry a content hash, so they can be cached hard.
		// This matters on the station WiFi, where phones reload the app all day.
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func fileExists(fsys fs.FS, name string) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()

	info, err := f.Stat()
	return err == nil && !info.IsDir()
}

// serveIndex returns the app shell. It is explicitly not cached: the shell
// names the hashed asset files, so a cached shell would pin a browser to the
// previous build after an upgrade.
func serveIndex(fsys fs.FS, w http.ResponseWriter, r *http.Request) {
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		http.Error(w, "the app shell is missing from this build", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	// Unknown paths are handled by the SPA router, which shows its own
	// not-found page; 200 is correct because the shell really was served.
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(index)
	}
}

// newDevProxy forwards everything the API does not handle to the Vite dev
// server, so hot reload works while the app is still reached on bokkie's own
// port — one origin in development and in production, which keeps cookies and
// relative API paths behaving the same in both.
func newDevProxy(target *url.URL, log *slog.Logger) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		log.Error("dev proxy: cannot reach the Vite dev server", "target", target.String(), "error", err)
		http.Error(w, "the Vite dev server is not running — start it with `make web-dev`", http.StatusBadGateway)
	}
	return proxy
}
