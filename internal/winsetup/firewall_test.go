package winsetup

import (
	"strings"
	"testing"
)

func TestParseProfiles(t *testing.T) {
	got := ParseProfiles("Station WiFi|Public\r\nOffice|Private\r\n\r\ngarbage\n")
	if len(got) != 2 || got[0] != (NetProfile{"Station WiFi", "Public"}) || got[1].Category != "Private" {
		t.Fatalf("got %+v", got)
	}
}

func TestFirewallVerdict(t *testing.T) {
	if _, blocked := FirewallVerdict([]NetProfile{{"Station", "Private"}}, true); blocked {
		t.Error("a private network with the rule in place is not blocked")
	}
	if _, blocked := FirewallVerdict([]NetProfile{{"Corp", "DomainAuthenticated"}}, true); blocked {
		t.Error("a domain network with the rule in place is not blocked")
	}
	b, blocked := FirewallVerdict([]NetProfile{{"Station WiFi", "Public"}}, true)
	if !blocked || !strings.Contains(b.Problem, "Station WiFi") || !strings.Contains(b.Fix, "Private") {
		t.Errorf("a public network must be reported with its name and the fix: %+v", b)
	}
	b, blocked = FirewallVerdict([]NetProfile{{"Station", "Private"}}, false)
	if !blocked || !strings.Contains(b.Fix, "BokkieSetup.exe") {
		t.Errorf("a missing rule must be reported: %+v", b)
	}
}
