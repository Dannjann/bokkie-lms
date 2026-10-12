package service

import (
	"strings"
	"testing"
)

// Found by the Windows runner on the first release, against code written in
// step 1 and never once executed: the SCM reads this list as service names,
// goes looking for a service called "After=network-online.target", and refuses
// to start the service it has just registered.
//
// The test runs on the developer's Mac because dependenciesFor takes the
// platform rather than reading it. That is the whole reason it does.
func TestWindowsIsNotHandedSystemdDirectivesAsServiceNames(t *testing.T) {
	for _, dep := range dependenciesFor("windows") {
		if strings.ContainsAny(dep, "=") {
			t.Errorf("Windows is told it depends on %q; the SCM reads that as a service name and refuses to start", dep)
		}
	}
	if got := dependenciesFor("windows"); len(got) != 0 {
		t.Errorf("dependenciesFor(windows) = %v, want none", got)
	}
}

// The systemd ordering is load-bearing on Linux: without it mDNS can start
// before there is a LAN address to advertise.
func TestLinuxStillWaitsForTheNetwork(t *testing.T) {
	got := dependenciesFor("linux")
	if len(got) != 2 || got[0] != "After=network-online.target" || got[1] != "Wants=network-online.target" {
		t.Errorf("dependenciesFor(linux) = %v, want the network-online ordering", got)
	}
}
