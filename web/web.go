// Package web embeds the built Vue SPA into the binary.
//
// The whole system ships as one executable, so the frontend travels inside it:
// there is no web root to copy onto the host and no way for the served assets
// to drift from the server that answers their API calls.
package web

import (
	"embed"
	"io/fs"
)

// The `all:` prefix matters — without it, files whose names begin with "_" or
// "." are skipped, and Vite emits them.
//
// dist/ is build output and is not in git; `make web/dist` writes a placeholder
// so a clean checkout still compiles. See docs/plans/01-foundation.md § 1.6.
//
//go:embed all:dist
var embedded embed.FS

// FS returns the built SPA rooted at dist, so paths are served as the browser
// asks for them ("/assets/index.js", not "/dist/assets/index.js").
func FS() (fs.FS, error) {
	return fs.Sub(embedded, "dist")
}
