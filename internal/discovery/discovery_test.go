package discovery

import (
	"net"
	"testing"
)

// Advertising a loopback or link-local address would send the other machines
// somewhere they cannot reach, which is worse than not advertising at all.
func TestLANAddressesAreReachableIPv4(t *testing.T) {
	addresses, err := lanAddresses()
	if err != nil {
		t.Fatalf("lanAddresses: %v", err)
	}

	for _, address := range addresses {
		ip := net.ParseIP(address)
		switch {
		case ip == nil:
			t.Errorf("%q is not an IP address", address)
		case ip.To4() == nil:
			t.Errorf("%s is not IPv4; the station is an IPv4 network", address)
		case ip.IsLoopback():
			t.Errorf("%s is loopback; no other machine can reach it", address)
		case ip.IsLinkLocalUnicast():
			t.Errorf("%s is link-local; it is not the station's address", address)
		}
	}
}
