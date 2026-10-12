//go:build windows

package winsetup

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// GetConsoleProcessList is not declared by golang.org/x/sys/windows, so it is
// declared here rather than pinning the project to a fork or a newer module for
// one call. kernel32 is already loaded in every process.
var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
)

// ConsoleProcessCount is how many processes share this process's console.
// Zero means there is no console at all, which is what Add/Remove Programs and
// the service manager give us.
func ConsoleProcessCount() int {
	// The call wants somewhere to write the process ids. We want only the
	// count, but it returns zero and sets ERROR_INSUFFICIENT_BUFFER when the
	// buffer is too small — so it has to be large enough for the answer to
	// mean what it says. Sixteen is far past any console we will meet.
	pids := make([]uint32, 16)
	n, _, _ := procGetConsoleProcessList.Call(
		uintptr(unsafe.Pointer(&pids[0])),
		uintptr(len(pids)),
	)
	return int(n)
}
