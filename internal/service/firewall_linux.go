//go:build linux

package service

import (
	"log/slog"
	"strconv"
)

// ensureFirewall opens the port with whichever firewall this distribution uses.
//
// A host with no firewall at all is not an error: plenty of minimal server
// installs have none, and inventing one during an install would be a surprising
// thing to do to someone's machine.
func ensureFirewall(log *slog.Logger, _ string, port int) error {
	rule := strconv.Itoa(port) + "/tcp"

	switch {
	case commandExists("ufw"):
		if err := runCommand(log, "ufw", "allow", rule); err != nil {
			return err
		}
	case commandExists("firewall-cmd"):
		if err := runCommand(log, "firewall-cmd", "--permanent", "--add-port="+rule); err != nil {
			return err
		}
		if err := runCommand(log, "firewall-cmd", "--reload"); err != nil {
			return err
		}
	default:
		log.Info("no ufw or firewalld found; assuming this host has no firewall to open", "port", port)
		return nil
	}

	log.Info("firewall opened", "port", port)
	return nil
}

func removeFirewall(log *slog.Logger, port int) error {
	rule := strconv.Itoa(port) + "/tcp"

	switch {
	case commandExists("ufw"):
		return runCommand(log, "ufw", "delete", "allow", rule)
	case commandExists("firewall-cmd"):
		if err := runCommand(log, "firewall-cmd", "--permanent", "--remove-port="+rule); err != nil {
			return err
		}
		return runCommand(log, "firewall-cmd", "--reload")
	default:
		return nil
	}
}
