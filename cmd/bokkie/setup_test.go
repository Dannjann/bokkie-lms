package main

import "testing"

func actions(choices []setupChoice) []setupAction {
	out := make([]setupAction, len(choices))
	for i, c := range choices {
		out[i] = c.Action
	}
	return out
}

func TestMenuOffersTheShortcutFirstOnAMachineWithNothingInstalled(t *testing.T) {
	got := menuFor(setupState{ThisVersion: "v1.0.0"})
	if got[0].Action != actionShortcut {
		t.Errorf("first choice = %v, want the shortcut: a counter PC must not be led into installing a second host", got[0].Action)
	}
	if got[0].NeedsAdmin {
		t.Error("the shortcut asked for administrator rights; it writes to one desktop and must not")
	}
}

// Running the same version again is a repair, not an update. Offering "update
// v1.0.0 to v1.0.0" is how somebody concludes the file they downloaded is wrong.
func TestMenuCallsTheSameVersionARepair(t *testing.T) {
	same := menuFor(setupState{Installed: true, InstalledVersion: "v1.0.0", ThisVersion: "v1.0.0"})
	if same[0].Action != actionRepair {
		t.Errorf("first choice = %v, want a repair when the versions match", same[0].Action)
	}

	newer := menuFor(setupState{Installed: true, InstalledVersion: "v1.0.0", ThisVersion: "v1.1.0"})
	if newer[0].Action != actionUpdate {
		t.Errorf("first choice = %v, want an update when the file is a different version", newer[0].Action)
	}
}

// A silent reinstall is the one thing the step forbids outright.
func TestMenuNeverOffersAPlainInstallOnAMachineThatHasOne(t *testing.T) {
	for _, a := range actions(menuFor(setupState{Installed: true, InstalledVersion: "v1.0.0", ThisVersion: "v1.1.0"})) {
		if a == actionInstall {
			t.Fatal("an installed machine was offered a plain install, which would reinstall over a running service without saying so")
		}
	}
}
