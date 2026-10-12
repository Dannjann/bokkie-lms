//go:build windows

package service

import (
	"fmt"
	"log/slog"
	"strconv"
)

// firewallRule is the name the rule is created and deleted by.
const firewallRule = "Bokkie LMS"

// ensureFirewall opens the port for the station's LAN.
//
// The rule is deleted first so that reinstalling on a different port does not
// leave the old one behind: netsh happily creates a second rule with the same
// name, and the stale one then quietly allows a port nothing is serving.
func ensureFirewall(log *slog.Logger, _ string, port int) error {
	_ = runCommand(log, "netsh", "advfirewall", "firewall", "delete", "rule", "name="+firewallRule)

	args := []string{
		"advfirewall", "firewall", "add", "rule",
		"name=" + firewallRule,
		"dir=in",
		"action=allow",
		"protocol=TCP",
		// Port 80 as well as the app's own: the server also answers there and
		// redirects, so that a counter PC given the bare name reaches the app
		// instead of nothing. A rule naming a port nothing serves is harmless.
		"localport=" + strconv.Itoa(port) + ",80",
		// Private and domain profiles only: the station LAN, never a public
		// network the host might later be plugged into.
		"profile=private,domain",
	}
	if err := runCommand(log, "netsh", args...); err != nil {
		return fmt.Errorf("open port %d in Windows Firewall: %w", port, err)
	}

	log.Info("firewall opened", "port", port, "rule", firewallRule)
	return nil
}

func removeFirewall(log *slog.Logger, _ int) error {
	return runCommand(log, "netsh", "advfirewall", "firewall", "delete", "rule", "name="+firewallRule)
}
