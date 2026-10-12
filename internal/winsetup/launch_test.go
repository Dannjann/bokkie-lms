package winsetup

import "testing"

func TestLaunchedFromExplorer(t *testing.T) {
	cases := []struct {
		name     string
		consoles int
		argc     int
		want     bool
	}{
		{"double-clicked", 1, 1, true},
		{"run from cmd with no command", 2, 1, false},
		{"double-clicked but given a command", 1, 2, false},
		{"a service start, which always has arguments", 0, 4, false},
		{"no console at all, as Add/Remove Programs gives us", 0, 2, false},
	}
	for _, c := range cases {
		if got := LaunchedFromExplorer(c.consoles, c.argc); got != c.want {
			t.Errorf("%s: LaunchedFromExplorer(%d, %d) = %v, want %v", c.name, c.consoles, c.argc, got, c.want)
		}
	}
}
