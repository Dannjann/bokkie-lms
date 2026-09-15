//go:build linux

package discovery

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/godbus/dbus/v5"
)

// Avahi's D-Bus interface. Registering through the responder that is already
// running is the only workable answer on a Linux host: Avahi holds port 5353 on
// virtually every desktop distribution, so a second responder cannot bind, and
// disabling Avahi would break everything else on the machine that relies on it.
const (
	avahiService      = "org.freedesktop.Avahi"
	avahiServerIface  = "org.freedesktop.Avahi.Server"
	avahiGroupIface   = "org.freedesktop.Avahi.EntryGroup"
	avahiIfaceUnspec  = int32(-1) // AVAHI_IF_UNSPEC: all interfaces
	avahiProtoUnspec  = int32(-1) // AVAHI_PROTO_UNSPEC: IPv4 and IPv6
	avahiNoPublishOpt = uint32(0) // no special publishing flags
)

// registerViaAvahi registers hostname.local and the HTTP service through Avahi.
// cause is the error from our own responder, kept so that a failure here can
// report both attempts rather than only the second one.
func registerViaAvahi(log *slog.Logger, hostname string, port int, addresses []string, cause error) (io.Closer, error) {
	log.Info("built-in mDNS responder could not bind; registering through Avahi instead",
		"reason", cause)

	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, fmt.Errorf("mDNS: built-in responder failed (%v) and the system D-Bus is unreachable: %w", cause, err)
	}

	group, err := newEntryGroup(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("mDNS: built-in responder failed (%v) and Avahi refused the registration: %w", cause, err)
	}

	fqdn := hostname + ".local"
	if err := publish(group, fqdn, port, addresses); err != nil {
		conn.Close()
		return nil, fmt.Errorf("mDNS via Avahi: %w", err)
	}

	log.Info("advertising over mDNS",
		"hostname", fqdn, "port", port, "addresses", addresses, "responder", "avahi")

	return closerFunc(func() error {
		// Free removes the records; without it the other machines keep being
		// sent to a host that is no longer listening.
		freeErr := group.Call(avahiGroupIface+".Free", 0).Err
		closeErr := conn.Close()
		if freeErr != nil {
			return freeErr
		}
		return closeErr
	}), nil
}

func newEntryGroup(conn *dbus.Conn) (dbus.BusObject, error) {
	var path dbus.ObjectPath
	if err := conn.Object(avahiService, "/").
		Call(avahiServerIface+".EntryGroupNew", 0).Store(&path); err != nil {
		return nil, fmt.Errorf("create entry group: %w", err)
	}
	return conn.Object(avahiService, path), nil
}

func publish(group dbus.BusObject, fqdn string, port int, addresses []string) error {
	for _, address := range addresses {
		if err := group.Call(avahiGroupIface+".AddAddress", 0,
			avahiIfaceUnspec, avahiProtoUnspec, avahiNoPublishOpt, fqdn, address).Err; err != nil {
			return fmt.Errorf("publish address %s for %s: %w", address, fqdn, err)
		}
	}

	txt := make([][]byte, 0, len(txtRecords))
	for _, record := range txtRecords {
		txt = append(txt, []byte(record))
	}
	if err := group.Call(avahiGroupIface+".AddService", 0,
		avahiIfaceUnspec, avahiProtoUnspec, avahiNoPublishOpt,
		instanceName, serviceType, "", fqdn, uint16(port), txt).Err; err != nil {
		return fmt.Errorf("publish service record: %w", err)
	}

	if err := group.Call(avahiGroupIface+".Commit", 0).Err; err != nil {
		return fmt.Errorf("commit records: %w", err)
	}
	return nil
}
