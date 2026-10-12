//go:build !windows

package winsetup

// Register and Deregister are Windows-only. They are no-ops rather than errors
// elsewhere so that the shared install path needs no build tag around them.
func Register(Program) error { return nil }

func Deregister() error { return nil }
