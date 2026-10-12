// Package discovery advertises the server on the station's LAN over mDNS, so
// every machine reaches it at a stable name.
//
// This is what makes `http://bokkie.local:8080` work without anyone touching a
// router: DHCP is free to move the host's address, and the shortcut on each
// counter PC keeps working.
//
// # Who owns port 5353
//
// The hard part is not the protocol, it is that most hosts already run an mDNS
// responder — mDNSResponder on macOS, Avahi on Linux — and two responders on
// one machine do not cooperate. Binding a second socket to 5353 appears to
// succeed and then quietly answers nothing, which is exactly the failure that
// was found on the first real install.
//
// So each platform registers through whatever already owns mDNS there, and the
// per-OS files below are that choice made explicit.
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

// instanceName is what a phone browsing the network sees in a list of services.
const instanceName = "Bokkie"

// txtRecords tells a browser where the app lives on the server it found.
var txtRecords = []string{"path=/"}

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
	return advertise(log, hostname, port, addresses)
}

// registerOwnResponder runs a responder of our own, for hosts where nothing
// else owns mDNS. It publishes the host record as well as the service record,
// because the name in the shortcut on every counter PC is what has to resolve.
func registerOwnResponder(log *slog.Logger, hostname string, port int, addresses []string) (io.Closer, error) {
	server, err := zeroconf.RegisterProxy(
		instanceName, serviceType, "local.", port, hostname, addresses, txtRecords, nil)
	if err != nil {
		return nil, err
	}

	log.Info("advertising over mDNS",
		"hostname", hostname+".local", "port", port, "addresses", addresses, "responder", "built-in")
	return closerFunc(func() error {
		server.Shutdown()
		return nil
	}), nil
}

// primaryAddress picks the address to advertise when only one can be. It asks
// the routing table which interface would be used to reach the LAN, rather than
// taking the first one listed: on a host with both WiFi and Ethernet, the first
// is as likely as not to be the one that is plugged into nothing.
func primaryAddress(addresses []string) string {
	conn, err := net.Dial("udp", "192.168.0.1:9")
	if err == nil {
		defer conn.Close()
		if local, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			for _, address := range addresses {
				if address == local.IP.String() {
					return address
				}
			}
		}
	}
	return addresses[0]
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
