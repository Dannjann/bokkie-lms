//go:build !windows

package main

import "fmt"

// runGuidedSetup is Windows-only. On macOS and Linux the install is the
// .command wrapper beside the binary, and saying so beats a window that offers
// nothing.
func runGuidedSetup([]string) error {
	return fmt.Errorf("the guided setup window is Windows-only; on this system run \"bokkie install\" with administrator rights, " +
		"or double-click \"Install Bokkie.command\" in the release folder")
}
