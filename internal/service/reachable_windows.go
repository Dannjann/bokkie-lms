//go:build windows

package service

// checkExecutableReachable has nothing to check on Windows: LocalService can
// read the usual install locations, and the home-directory trap that makes this
// necessary on macOS and Linux does not apply — a service installed from a user
// profile fails loudly at start rather than silently at exec.
func checkExecutableReachable(_, _ string) error {
	return nil
}
