//go:build !windows

package winsetup

// CheckFirewall is a Windows question; elsewhere it is never asked.
func CheckFirewall() (b Blocked, blocked, ok bool) { return Blocked{}, false, false }
