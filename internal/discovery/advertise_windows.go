//go:build windows

package discovery

import (
	"io"
	"log/slog"
)

// advertise runs our own responder. Windows has no system mDNS daemon holding
// port 5353 the way macOS and Linux do — unless Bonjour was installed with
// iTunes, which is why a failure here is reported rather than worked around.
func advertise(log *slog.Logger, hostname string, port int, addresses []string) (io.Closer, error) {
	return registerOwnResponder(log, hostname, port, addresses)
}
