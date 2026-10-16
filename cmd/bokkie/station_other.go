//go:build !windows && !darwin

package main

import "github.com/Dannjann/bokkie-lms/internal/app"

// runStationWindow is Windows and macOS only. On Linux serve keeps its browser
// window, and a Linux station runs as a service anyway.
func runStationWindow(string, app.Options) error { return errNoStationWindow }

func openedAsApp() bool { return false }
