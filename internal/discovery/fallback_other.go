//go:build !linux

package discovery

import (
	"io"
	"log/slog"
)

// advertiseFallback exists only on Linux, where Avahi normally owns port 5353.
// Everywhere else a failure to bind is a real failure and is reported as one.
func advertiseFallback(_ *slog.Logger, _ string, _ int, _ []string, cause error) (io.Closer, error) {
	return nil, cause
}
