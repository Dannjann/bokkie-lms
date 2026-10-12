//go:build !windows

package winsetup

// ConsoleProcessCount is zero everywhere else: the guided window is a Windows
// affordance, and macOS and Linux installs go through the .command wrappers.
func ConsoleProcessCount() int { return 0 }
