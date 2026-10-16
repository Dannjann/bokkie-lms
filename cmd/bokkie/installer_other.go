//go:build !windows

package main

import "errors"

func cmdInstallerHook([]string) error {
	return errors.New("installer-hook is part of the Windows installer")
}
