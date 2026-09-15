//go:build darwin

package config

// defaultDataDir is the system-wide Application Support directory, not the one
// under ~/Library: the launchd daemon runs as _bokkie with nobody logged in, so
// a per-user path would be both unreachable and wrong.
func defaultDataDir() (string, error) {
	return "/Library/Application Support/Bokkie", nil
}
