package winsetup

import "testing"

// A hard requirement to not do five things erodes one convenience at a time.
// This asserts the list of things being checked has not quietly shrunk.
func TestEveryAutoStartMechanismIsNamed(t *testing.T) {
	want := []string{"service", "run key", "run once key", "startup folder", "scheduled task"}

	got := map[string]bool{}
	for _, name := range AutoStartNames() {
		got[name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("%q is no longer checked; something could start the station without a person", name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("AutoStartNames has %d entries, want %d", len(got), len(want))
	}
}

// On Windows the lookups must cover exactly the named mechanisms. Elsewhere
// there are none, which is the point of the build-tagged file.
func TestTheLookupsCoverEveryNamedMechanism(t *testing.T) {
	mechanisms := AutoStartMechanisms()
	if len(mechanisms) == 0 {
		t.Skip("not Windows; the mechanisms are a Windows station's rule")
	}

	covered := map[string]bool{}
	for _, m := range mechanisms {
		covered[m.Name] = true
		if m.Where == "" {
			t.Errorf("%q does not say where to look; a person cannot check it by hand", m.Name)
		}
	}
	for _, name := range AutoStartNames() {
		if !covered[name] {
			t.Errorf("%q is named but never looked for", name)
		}
	}
}

func TestFoundReturnsOnlyWhatIsPresent(t *testing.T) {
	got := Found([]AutoStart{
		{Name: "service", Present: true},
		{Name: "run key", Present: false},
		{Name: "scheduled task", Present: true},
	})
	if len(got) != 2 {
		t.Fatalf("Found returned %d, want 2", len(got))
	}
}
