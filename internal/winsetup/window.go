package winsetup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// WindowArgs opens a browser in application mode — no address bar, no tabs — on
// its own profile directory.
//
// --user-data-dir is the load-bearing one, and it is not about the profile. A
// browser that is already running treats a second launch as "open a window in
// the existing process" and the new process exits immediately. Waiting on that
// process would then stop the station about a second after it started, on every
// machine where somebody had a browser open — which is every machine. Its own
// profile directory forces its own process, which is a thing that can be waited
// on.
func WindowArgs(address, profileDir string) []string {
	if profileDir == "" {
		// An empty value is ignored by the browser, which is the same bug as
		// omitting the flag. A caller that forgot gets a real directory rather
		// than a station that stops a second after it starts.
		profileDir = filepath.Join(os.TempDir(), "bokkie-window")
	}
	return []string{
		"--app=" + address,
		"--user-data-dir=" + profileDir,
		// Nothing in this window should offer to become a normal browser.
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
	}
}

// BrowserCommands are tried in order. Edge first: it is what Windows has, what
// the counter PCs already use, and what FindEdge already knows how to locate.
func BrowserCommands() []string {
	return []string{"msedge.exe", "chrome.exe"}
}

// OpenWindow starts a browser window on address and returns its process, so the
// caller can wait on it.
//
// Edge's real path is tried first through FindEdge, because a station may not
// have it on PATH. Everything after that is a fallback for a developer machine.
func OpenWindow(address, profileDir string) (*os.Process, error) {
	var candidates []string
	if edge, err := FindEdge(); err == nil {
		candidates = append(candidates, edge)
	}
	candidates = append(candidates, BrowserCommands()...)

	var lastErr error
	for _, browser := range candidates {
		cmd := exec.Command(browser, WindowArgs(address, profileDir)...)
		if err := cmd.Start(); err != nil {
			lastErr = err
			continue
		}
		return cmd.Process, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no browser found")
	}
	return nil, fmt.Errorf("open a window on %s: %w", address, lastErr)
}
