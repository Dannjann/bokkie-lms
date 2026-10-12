package winsetup

import (
	"strings"
	"testing"
)

// C:\Program Files is the only place this will ever point, and an unquoted
// path there is read by Windows as C:\Program with arguments.
func TestUninstallStringQuotesThePath(t *testing.T) {
	p := Program{
		UninstallExe:  `C:\Program Files\Bokkie\bokkie.exe`,
		UninstallArgs: []string{"uninstall", "--from-control-panel"},
	}
	got := p.Values()["UninstallString"]
	want := `"C:\Program Files\Bokkie\bokkie.exe" uninstall --from-control-panel`
	if got != want {
		t.Errorf("UninstallString = %s, want %s", got, want)
	}
	// The closing quote must come before the arguments, not after them: with
	// both inside, Windows reads the whole string as one very long file name.
	if closing := strings.Index(got[1:], `"`) + 1; closing != len(p.UninstallExe)+1 {
		t.Errorf("the closing quote is at %d, want %d — the arguments are inside the quotes", closing, len(p.UninstallExe)+1)
	}
}

func TestValuesCarryTheVersionSoControlPanelCanShowIt(t *testing.T) {
	got := Program{Name: "Bokkie LMS", Version: "v1.0.0"}.Values()
	if got["DisplayName"] != "Bokkie LMS" || got["DisplayVersion"] != "v1.0.0" {
		t.Errorf("Values = %v, want the name and version Control Panel lists", got)
	}
}
