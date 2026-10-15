package winsetup

// AutoStart is one way Windows can start something without being asked.
//
// They are enumerated rather than merely avoided. Running only when a person
// opens the window is a hard requirement, and a requirement to not do five
// things is kept by a test that goes and looks for all five — not by everybody
// remembering. See D1 in docs/plans/02.7-window-is-the-server.md.
type AutoStart struct {
	// Name is what it is called, for the message.
	Name string
	// Where is the exact location, so a person can check it by hand.
	Where string
	// Present is whether bokkie is currently in it.
	Present bool
}

// AutoStartNames is every mechanism that must be checked. It is a separate
// list from the lookups so that a test can assert the list itself has not
// quietly shrunk — which is how a requirement like this erodes.
func AutoStartNames() []string {
	return []string{"service", "run key", "run once key", "startup folder", "scheduled task"}
}

// Found returns only the mechanisms bokkie is actually in.
func Found(all []AutoStart) []AutoStart {
	var out []AutoStart
	for _, m := range all {
		if m.Present {
			out = append(out, m)
		}
	}
	return out
}
