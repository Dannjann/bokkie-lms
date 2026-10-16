package winsetup

import "strings"

// FirewallRule is the name the installer gives the rule that lets the counter
// PCs in. It is the name 2.6's service install used too, so either one is
// found and replaced.
const FirewallRule = "Bokkie LMS"

// NetProfile is one network this computer is connected to, as Windows
// classifies it: Public, Private or DomainAuthenticated.
type NetProfile struct{ Name, Category string }

// Blocked is why Windows is keeping the other computers out, and what to do.
type Blocked struct{ Problem, Fix string }

// ParseProfiles reads the "name|category" lines the probe prints.
func ParseProfiles(out string) []NetProfile {
	var profiles []NetProfile
	for _, line := range strings.Split(out, "\n") {
		name, category, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok {
			continue
		}
		profiles = append(profiles, NetProfile{Name: strings.TrimSpace(name), Category: strings.TrimSpace(category)})
	}
	return profiles
}

// FirewallVerdict decides whether Windows Firewall is what stands between the
// counter PCs and the station.
//
// Two ways it is. Windows calls every network it has not been told about
// "Public", and the rule deliberately does not cover public networks — so a
// station on a Wi-Fi Windows has never been told is trusted answers on
// 127.0.0.1 and to nobody else. Or the rule is not there at all, because
// Bokkie was never installed with the installer.
func FirewallVerdict(profiles []NetProfile, ruleExists bool) (Blocked, bool) {
	var public []string
	for _, p := range profiles {
		if strings.EqualFold(p.Category, "Public") {
			public = append(public, p.Name)
		}
	}
	if len(public) > 0 {
		return Blocked{
			Problem: "Windows treats the network \"" + strings.Join(public, "\", \"") + "\" as Public, " +
				"so it blocks the other computers from reaching Bokkie.",
			Fix: "On this computer open Settings > Network & internet, click the network it is " +
				"connected to (Wi-Fi or Ethernet), and set \"Network profile type\" to Private. " +
				"Do this only for the station's own network.",
		}, true
	}
	if !ruleExists {
		return Blocked{
			Problem: "Windows Firewall has no rule letting the other computers reach Bokkie.",
			Fix:     "Run BokkieSetup.exe again on this computer and choose Station computer; it adds the rule.",
		}, true
	}
	return Blocked{}, false
}
