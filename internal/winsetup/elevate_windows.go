//go:build windows

package winsetup

import (
	"errors"
	"fmt"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// Elevate restarts exe with administrator rights and returns as soon as
// Windows has accepted the request.
//
// It does not wait for the child: the parent's console closes while the child's
// opens, and a parent that lingered would leave two windows, one of them behind
// the other, both looking like the install.
func Elevate(exe string, args []string) error {
	verb, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	params, err := syscall.UTF16PtrFromString(strings.Join(args, " "))
	if err != nil {
		return err
	}

	err = windows.ShellExecute(0, verb, file, params, nil, windows.SW_NORMAL)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, windows.ERROR_CANCELLED):
		return ErrElevationCancelled
	default:
		return fmt.Errorf("ask Windows for administrator rights: %w", err)
	}
}

// IsElevated reports whether this process is already running as an
// administrator.
func IsElevated() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY, 2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0, &sid)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	member, err := windows.Token(0).IsMember(sid)
	return err == nil && member
}

// Report says how it went in a window rather than on a console, for the paths
// Windows starts with no console behind them — the Add/Remove Programs entry.
func Report(title, message string) {
	caption, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	text, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		return
	}
	windows.MessageBox(0, text, caption, windows.MB_OK|windows.MB_SETFOREGROUND)
}
