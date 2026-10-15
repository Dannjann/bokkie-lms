//go:build !windows

package winsetup

// AutoStartMechanisms is empty everywhere but Windows. macOS and Linux hosts
// still run bokkie under their own service manager by design — the hard
// requirement that a person opens the window is a Windows station's rule, and
// the launchd and systemd paths are the way back to an unattended host.
func AutoStartMechanisms() []AutoStart { return nil }
