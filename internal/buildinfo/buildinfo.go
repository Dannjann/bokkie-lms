// Package buildinfo reports which build of bokkie is actually running.
//
// The first question on any support call is "which binary is on that machine?"
// — these values are stamped in at link time by the Makefile so the answer is
// always one `bokkie version` away.
package buildinfo

import (
	"fmt"
	"runtime"
)

// Overridden via -ldflags -X at build time. The defaults are what you see when
// the binary was built without the Makefile (go run, plain go build), which is
// itself useful information.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String is the one-line build identity: version, provenance, and the platform
// it was cross-compiled for.
func String() string {
	return fmt.Sprintf("bokkie %s (commit %s, built %s) %s/%s %s",
		Version, Commit, Date, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
