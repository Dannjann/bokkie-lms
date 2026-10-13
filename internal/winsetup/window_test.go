package winsetup

import (
	"strings"
	"testing"
)

// Review Focus 1. Without its own profile directory a second launch joins the
// browser already running and exits at once, and the station would stop a
// second after it started on every machine where somebody had a browser open.
func TestWindowArgsForceTheirOwnBrowserProcess(t *testing.T) {
	args := WindowArgs("http://localhost:8080", `C:\Users\tls\AppData\Local\Temp\bokkie-window`)

	var hasProfile, hasApp bool
	for _, a := range args {
		if strings.HasPrefix(a, "--user-data-dir=") && len(a) > len("--user-data-dir=") {
			hasProfile = true
		}
		if a == "--app=http://localhost:8080" {
			hasApp = true
		}
	}
	if !hasProfile {
		t.Error("no --user-data-dir: the window would join a browser already running and exit immediately, stopping the station")
	}
	if !hasApp {
		t.Error("no --app=: the window would open as an ordinary tab, with an address bar to navigate away from")
	}
}

// An empty profile directory is the same bug as no flag at all, and is the
// shape a caller's mistake would take.
func TestWindowArgsRejectAnEmptyProfileDirectory(t *testing.T) {
	for _, a := range WindowArgs("http://localhost:8080", "") {
		if a == "--user-data-dir=" {
			t.Error("an empty --user-data-dir was passed; the browser ignores it and joins the running process")
		}
	}
}
