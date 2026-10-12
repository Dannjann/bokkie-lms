//go:build !windows

package winsetup

import "fmt"

// The desktop shortcut is a Windows affordance: it opens Edge in application
// mode, which is what makes the app look like a program rather than a web page
// to the people standing at the counter.
func FindEdge() (string, error) {
	return "", fmt.Errorf("the desktop shortcut is Windows-only")
}

func CreateDesktopShortcut(string) (string, error) {
	return "", fmt.Errorf("the desktop shortcut is Windows-only")
}
