//go:build windows

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/Dannjann/bokkie-lms/internal/winsetup"
)

// errHandedOff means the work is now the elevated window's, and this one should
// close without claiming anything happened.
var errHandedOff = errors.New("handed off to the administrator window")

// runGuidedSetup is the window a double-click opens.
//
// It runs twice per install: once unelevated, to show the menu and find out
// what is wanted, and once as administrator with --do naming the answer. The
// parts that need no administrator — the desktop shortcut — are done in the
// first pass, before any prompt is raised.
func runGuidedSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	elevated := fs.Bool("elevated", false, "this process was started by the unelevated window")
	do := fs.String("do", "", "the action chosen in the unelevated window")
	if err := fs.Parse(args); err != nil {
		return err
	}

	done, err := guided(*elevated, *do)

	// errHandedOff is the one case that must stay silent: this window raised a
	// UAC prompt and the elevated copy now owns the job. Two windows holding
	// for Enter, one behind the other, is how an install looks hung.
	if errors.Is(err, errHandedOff) {
		return nil
	}

	fmt.Println()
	fmt.Println(winsetup.ExitMessage(err, done))
	winsetup.PressEnterToClose(os.Stdout, os.Stdin)
	return err
}

// guided is filled in by task 3.
func guided(elevated bool, do string) (string, error) {
	return "", fmt.Errorf("task 3")
}
