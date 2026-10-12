//go:build linux

package config

// defaultDataDir follows the FHS convention for state a daemon owns and must
// survive reboots. The systemd unit runs as the dedicated bokkie user, which
// owns this directory.
func defaultDataDir() (string, error) {
	return "/var/lib/bokkie", nil
}
