//go:build windows

package winsetup

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// Register puts bokkie in Add/Remove Programs. It needs administrator rights,
// which the install it follows has already obtained.
func Register(p Program) error {
	key, _, err := registry.CreateKey(registry.LOCAL_MACHINE, KeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open the Add/Remove Programs key: %w", err)
	}
	defer key.Close()

	for name, value := range p.Values() {
		if err := key.SetStringValue(name, value); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

// Deregister takes the entry back out.
//
// A key that was never there is not a failure: an uninstall run against an
// install from before this existed must still finish.
func Deregister() error {
	err := registry.DeleteKey(registry.LOCAL_MACHINE, KeyPath)
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}
