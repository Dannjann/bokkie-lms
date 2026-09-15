// Package discovery advertises the server on the station's LAN over mDNS, so
// every machine reaches it at a stable name.
//
// This is what makes `http://bokkie.local:8080` work without anyone touching a
// router: DHCP is free to move the host's address, and the shortcut on each
// counter PC keeps working.
package discovery

import (
	"fmt"
	"io"
	"log/slog"
	"net"

	"github.com/grandcat/zeroconf"
)

// serviceType is the standard "a web server lives here" mDNS type. Browsing for
// it is how a phone or a support technician finds the station's host without
// being told its address.
const serviceType = "_http._tcp"

// Advertise publishes hostname.local and an HTTP service record for port.
//
// The returned Closer must be closed on shutdown; leaving stale records behind
// sends the other machines to an address nothing is listening on.
func Advertise(log *slog.Logger, hostname string, port int) (io.Closer, error) {
	addresses, err := lanAddresses()
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("no LAN address to advertise; is the host connected to the station network?")
	}

	server, err := zeroconf.RegisterProxy(
		"Bokkie", serviceType, "local.", port, hostname, addresses, []string{"path=/"}, nil)
	if err != nil {
		// On Linux this is almost always Avahi already holding port 5353. It is
		// the expected case there, not an exceptional one.
		closer, fallbackErr := advertiseFallback(log, hostname, port, addresses, err)
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		return closer, nil
	}

	log.Info("advertising over mDNS",
		"hostname", hostname+".local", "port", port, "addresses", addresses, "responder", "built-in")
	return closerFunc(func() error {
		server.Shutdown()
		return nil
	}), nil
}

// lanAddresses collects the IPv4 addresses worth advertising: the station is an
// IPv4 network, and advertising a link-local or loopback address would send the
// other machines somewhere they cannot reach.
func lanAddresses() ([]string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list network interfaces: %w", err)
	}

	var addresses []string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue // one unreadable interface must not stop the rest
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLinkLocalUnicast() || ip.IsLoopback() {
				continue
			}
			addresses = append(addresses, ip.String())
		}
	}
	return addresses, nil
}

// closerFunc adapts a shutdown function to io.Closer.
type closerFunc func() error

func (f closerFunc) Close() error { return f() }
