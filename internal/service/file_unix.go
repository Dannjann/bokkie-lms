//go:build darwin || linux

package service

import "os"

// fileIsExecutable reports whether path exists and can be executed, used where
// distributions disagree about where a standard tool lives.
func fileIsExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}
