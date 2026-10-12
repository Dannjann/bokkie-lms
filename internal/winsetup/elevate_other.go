//go:build !windows

package winsetup

import "fmt"

// Elevate is Windows-only; everywhere else sudo is the operator's own business.
func Elevate(string, []string) error {
	return fmt.Errorf("administrator elevation is a Windows affordance")
}

// IsElevated is true for root and effectively true for anyone who got this far
// on a Unix host, where the .command wrapper has already asked for a password.
func IsElevated() bool { return true }

// Report is a printed line where there is a console to print it on.
func Report(title, message string) { fmt.Printf("%s: %s\n", title, message) }
