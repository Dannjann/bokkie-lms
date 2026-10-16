//go:build windows

package winsetup

import (
	"os/exec"
	"syscall"
)

// CheckFirewall asks Windows how it classifies the networks this computer is
// on, and whether the installer's rule is there. ok is false when either
// question could not be asked; nothing is then claimed either way.
//
// PowerShell and netsh rather than COM: both are on every Windows this will
// meet, neither needs administrator rights to read, and the alternative is an
// OLE binding in a project that has none.
func CheckFirewall() (b Blocked, blocked, ok bool) {
	out, err := hidden(exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		`Get-NetConnectionProfile | ForEach-Object { "$($_.Name)|$($_.NetworkCategory)" }`)).Output()
	if err != nil {
		return Blocked{}, false, false
	}
	rule := hidden(exec.Command("netsh", "advfirewall", "firewall", "show", "rule", "name="+FirewallRule)).Run() == nil
	b, blocked = FirewallVerdict(ParseProfiles(string(out)), rule)
	return b, blocked, true
}

// hidden keeps a console program from flashing a window over the station's.
func hidden(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd
}
