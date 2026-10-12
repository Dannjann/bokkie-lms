//go:build windows

package config

import (
	"os"
	"path/filepath"
)

// defaultDataDir is C:\ProgramData\Bokkie — machine-wide rather than per-user,
// because the service runs as LocalService and no human is logged in when it
// starts. ProgramData comes from the environment rather than being hardcoded:
// on a machine whose system drive is not C:, the hardcoded path is wrong.
func defaultDataDir() (string, error) {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "Bokkie"), nil
}
