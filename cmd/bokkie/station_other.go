//go:build !windows

package main

import "github.com/Dannjann/bokkie-lms/internal/app"

// runStationWindow is Windows only. Elsewhere serve keeps its browser window,
// and a Mac or Linux station runs as a service anyway.
func runStationWindow(string, app.Options) error { return errNoStationWindow }
