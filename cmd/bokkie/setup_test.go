package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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

func TestDescribeInstallationPrefersWhatIsAnswering(t *testing.T) {
	state := describeInstallation(`C:\Users\dan\Downloads\BokkieSetup.exe`, `C:\Program Files\Bokkie\bokkie.exe`, "v1.0.0", nil)
	if !state.Installed || state.InstalledVersion != "v1.0.0" {
		t.Errorf("state = %+v, want installed at v1.0.0 from the serving version", state)
	}
}

// A binary at the install path that answers nothing is a failed update. It is
// still installed, and repair is exactly what it needs.
func TestDescribeInstallationCountsASilentBinaryAsInstalled(t *testing.T) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "bokkie.exe")
	if err := os.WriteFile(installed, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	state := describeInstallation(filepath.Join(dir, "download.exe"), installed, "", errors.New("connection refused"))
	if !state.Installed {
		t.Error("a binary at the install path that is not answering was reported as not installed")
	}
	if menuFor(state)[0].Action != actionRepair {
		t.Error("a failed install was not offered a repair")
	}
}

// Review Focus 4: double-clicking the installed copy must be recognised, or
// the install would try to copy a file over itself.
func TestDescribeInstallationNoticesItIsTheInstalledCopy(t *testing.T) {
	state := describeInstallation(`C:\Program Files\Bokkie\bokkie.exe`, `C:\PROGRAM FILES\BOKKIE\bokkie.exe`, "v1.0.0", nil)
	if !state.RunningFromInstallPath {
		t.Error("running from the install path was not noticed; Windows paths differ in case, not identity")
	}
}

func TestEveryActionSurvivesTheTripThroughElevation(t *testing.T) {
	for _, a := range []setupAction{actionInstall, actionUpdate, actionRepair, actionShortcut, actionUninstall} {
		if got := actionFor(nameOf(a)); got != a {
			t.Errorf("actionFor(nameOf(%v)) = %v; the elevated window would do the wrong thing", a, got)
		}
	}
}

// An unrecognised --do must not fall through to install. It is the one action
// that would make a counter PC into a second host.
func TestAnUnknownActionIsNotAnInstall(t *testing.T) {
	if actionFor("instal") == actionInstall {
		t.Fatal("a misspelled action was read as an install")
	}
	if actionFor("") == actionInstall {
		t.Fatal("an empty action was read as an install")
	}
}

// The shortcut is what a counter PC runs, and asking it for an administrator
// password is asking the person at the counter for one they do not have.
func TestTheShortcutNeverRaisesAPrompt(t *testing.T) {
	if needsAdmin(actionShortcut) {
		t.Error("the desktop shortcut was marked as needing administrator rights")
	}
	for _, a := range []setupAction{actionInstall, actionUpdate, actionRepair, actionUninstall} {
		if !needsAdmin(a) {
			t.Errorf("%v was not marked as needing administrator rights", a)
		}
	}
}

func TestProgramEntryPointsAtTheInstalledBinary(t *testing.T) {
	// Built with filepath.Join rather than written as a Windows path:
	// filepath.Dir only understands the host's separator, and this test is
	// about what programEntry does, not about running it on the wrong OS.
	dir := filepath.Join("Program Files", "Bokkie")
	exe := filepath.Join(dir, "bokkie.exe")

	got := programEntry(exe)
	if got.InstallLocation != dir {
		t.Errorf("InstallLocation = %q, want %q", got.InstallLocation, dir)
	}
	if want := `"` + exe + `" uninstall --from-control-panel`; got.Values()["UninstallString"] != want {
		t.Errorf("UninstallString = %q, want %q", got.Values()["UninstallString"], want)
	}
}
