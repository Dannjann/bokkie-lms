//go:build windows

package main

// runGuidedSetup is filled in by tasks 2 and 3. It exists now so main.go
// compiles and `go vet GOOS=windows` has something to check.
func runGuidedSetup(args []string) error {
	panic("task 2")
}
