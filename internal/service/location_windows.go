//go:build windows

package service

import (
	"fmt"
	"os"
	"path/filepath"
)

// installPath is where `install` puts the binary, and the path the service
// manager is handed to run at every boot from now on.
//
// %ProgramFiles% rather than a literal C:\Program Files: a machine whose system
// drive is not C:, or a Windows installed in another language, has the folder
// somewhere else, and a hardcoded path would install into a directory that does
// not exist rather than failing usefully.
func defaultInstallPath() (string, error) {
	base := os.Getenv("ProgramFiles")
	if base == "" {
		return "", fmt.Errorf("%%ProgramFiles%% is not set, so there is nowhere obvious to install bokkie; " +
			"pass the binary's final location yourself by copying it there and installing from it")
	}
	return filepath.Join(base, "Bokkie", "bokkie.exe"), nil
}
