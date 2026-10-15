//go:build !windows

package winsetup

import "fmt"

// Grant is a Windows affordance. On macOS and Linux the data folder belongs to
// root and the install already asked for a password, so there is no equivalent
// situation to repair.
func Grant(string) error {
	return fmt.Errorf("repairing folder permissions is Windows-only")
}
