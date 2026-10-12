//go:build darwin || linux

package service

// installPath is where `install` puts the binary, and the path the service
// manager is handed to run at every boot from now on.
//
// /usr/local/bin is root-owned and world-readable on both macOS and Linux,
// which is what makes checkExecutableReachable pass by construction: the
// service account can traverse every directory from / downward and read the
// file. That check exists because the obvious thing to type is
// `sudo ./bokkie install` from a 0700 home directory, and the resulting
// service has nothing it can execute.
func defaultInstallPath() (string, error) {
	return "/usr/local/bin/bokkie", nil
}
