package winsetup

import (
	"strings"
	"testing"
)

func TestShortcutScriptLaunchesEdgeInAppMode(t *testing.T) {
	got := ShortcutScript(`C:\Program Files\Microsoft\Edge\Application\msedge.exe`, DefaultAddress, `C:\Users\tls\Desktop\Bokkie.lnk`)
	for _, want := range []string{"--app=http://bokkie.local:8080", "msedge.exe", "Bokkie.lnk", "$s.Save()"} {
		if !strings.Contains(got, want) {
			t.Errorf("the script is missing %q:\n%s", want, got)
		}
	}
}

// The address can come from the operator when bokkie.local does not resolve,
// which makes it the one piece of this string that is not ours.
func TestShortcutScriptClosesAnInjectedQuote(t *testing.T) {
	got := ShortcutScript("edge.exe", `http://x'; Remove-Item C:\ -Recurse; '`, "s.lnk")
	if strings.Contains(got, `'; Remove-Item C:\ -Recurse; ';`) {
		t.Errorf("an apostrophe in the address escaped its string:\n%s", got)
	}
	if !strings.Contains(got, "''") {
		t.Errorf("the apostrophe was not doubled:\n%s", got)
	}
}
