//go:build windows

package winsetup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// FindEdge looks where Edge actually is. The 32-bit location is checked first
// because that is where it lives on a 64-bit Windows, which is every machine
// this will meet.
func FindEdge() (string, error) {
	for _, base := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")} {
		if base == "" {
			continue
		}
		path := filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("could not find Microsoft Edge in the usual place; send the developer this message")
}

// CreateDesktopShortcut puts a Bokkie shortcut on the desktop of whoever is
// running this, and returns where it put it.
//
// The desktop comes from the known folder rather than %USERPROFILE%\Desktop:
// a machine with OneDrive redirecting the desktop has the second one, and the
// shortcut would land somewhere the person is not looking.
func CreateDesktopShortcut(address string) (string, error) {
	edge, err := FindEdge()
	if err != nil {
		return "", err
	}
	desktop, err := windows.KnownFolderPath(windows.FOLDERID_Desktop, 0)
	if err != nil {
		return "", fmt.Errorf("find this computer's desktop: %w", err)
	}

	lnk := filepath.Join(desktop, "Bokkie.lnk")
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass",
		"-Command", ShortcutScript(edge, address, lnk))
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("create the shortcut: %w: %s", err, out)
	}
	return lnk, nil
}
