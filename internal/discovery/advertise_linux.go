//go:build linux

package discovery

import (
	"io"
	"log/slog"
)

// advertise prefers our own responder and falls back to Avahi.
//
// The order is this way round because a minimal server install — the dedicated
// mini-PC or Pi this system is happiest on — often has no Avahi at all, while a
// desktop distribution almost always does and already holds port 5353.
func advertise(log *slog.Logger, hostname string, port int, addresses []string) (io.Closer, error) {
	closer, err := registerOwnResponder(log, hostname, port, addresses)
	if err == nil {
		return closer, nil
	}
	return registerViaAvahi(log, hostname, port, addresses, err)
}
