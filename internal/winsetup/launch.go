// Package winsetup is the Windows desktop integration a double-clicked bokkie
// needs: deciding it was double-clicked at all, asking for administrator
// rights, putting a shortcut on a desktop, and appearing in Add/Remove
// Programs.
//
// Every decision in it is a pure function in an untagged file, and every
// //go:build windows file is a thin wrapper around one syscall. That split is
// not tidiness: no Windows machine has ever been available to this project, so
// anything holding a judgement must be testable on the developer's Mac.
package winsetup

// LaunchedFromExplorer reports whether this process was started by
// double-clicking it rather than typed at a prompt.
//
// One process attached to the console means the console was created for us —
// Explorer makes a fresh one per process, while cmd.exe shares its own, so a
// binary run from a prompt always sees at least two. Arguments settle the rest:
// anything with a command on it was typed, whatever created the window.
func LaunchedFromExplorer(consoleProcesses, argc int) bool {
	return argc < 2 && consoleProcesses == 1
}
