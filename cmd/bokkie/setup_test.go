package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/release"
	"github.com/Dannjann/bokkie-lms/internal/winsetup"
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

func TestASelfCheckFailureNeverReplacesAWorkingStation(t *testing.T) {
	proceed, notice := weighSelf(true, selfCheck{}, errors.New("no such host"))
	if proceed {
		t.Fatal("an unverified file was allowed to update an installed station")
	}
	if !strings.Contains(notice.Error(), "Nothing has been changed") {
		t.Errorf("the refusal did not say the machine is untouched:\n%s", notice)
	}
	if !strings.Contains(notice.Error(), "USB stick") {
		t.Errorf("the refusal did not offer the way that still works:\n%s", notice)
	}
}

// Review Focus 3: the offline station is the reason the USB path still exists,
// and refusing a first install for having no internet makes it uninstallable.
func TestAnOfflineFirstInstallProceedsAndSaysSo(t *testing.T) {
	proceed, notice := weighSelf(false, selfCheck{}, errors.New("dial tcp: i/o timeout"))
	if !proceed {
		t.Fatal("a first install was refused for having no internet, which makes an offline station uninstallable")
	}
	if !strings.Contains(notice.Error(), "could not be checked") {
		t.Errorf("the install did not admit it was unchecked:\n%s", notice)
	}
}

func TestAVerifiedFileIsNotTalkedAbout(t *testing.T) {
	proceed, notice := weighSelf(true, selfCheck{OK: true}, nil)
	if !proceed || notice != nil {
		t.Errorf("a verified file produced proceed=%v notice=%v, want a silent yes", proceed, notice)
	}
}

// The finding this replaces: the refusal was printed and the install ran
// anyway, so the screen said "Nothing has been changed" and then the service
// was torn down and replaced with the unverified bytes.
func TestBytesThatDoNotMatchTheReleaseAreRefusedEverywhere(t *testing.T) {
	altered := selfCheck{Reason: "bokkie.exe has been altered or corrupted since it was built"}

	for _, installed := range []bool{true, false} {
		proceed, notice := weighSelf(installed, altered, nil)
		if proceed {
			t.Fatalf("installed=%v: a file whose bytes do not match the release it claims to be was allowed to install", installed)
		}
		if !strings.Contains(notice.Error(), "does not match") {
			t.Errorf("installed=%v: the refusal does not say what is wrong:\n%s", installed, notice)
		}
	}
}

// Review Focus 2, at the call site rather than only inside verifySelf. The
// window must print the version sentence and nothing alarming around it.
func TestAStaleDownloadIsAVerdictNotAFault(t *testing.T) {
	stale := selfCheck{Stale: true, Reason: "This file is Bokkie v1.0.0, and the current release is v1.1.0."}

	proceed, notice := weighSelf(true, stale, nil)
	if proceed {
		t.Error("an older release was allowed over a working station")
	}
	printed := winsetup.ExitMessage(notice, "")
	if strings.Contains(printed, "Send the developer") || strings.Contains(printed, "could not be checked") {
		t.Errorf("an out-of-date download was reported as a fault:\n%s", printed)
	}
	if !strings.Contains(printed, "v1.1.0") {
		t.Errorf("the window does not name the version to download:\n%s", printed)
	}

	// A bare machine has nothing to lose, so an older release still installs.
	if proceed, _ := weighSelf(false, stale, nil); !proceed {
		t.Error("an older release was refused on a machine with nothing on it")
	}
}

// Review Focus 2: an out-of-date download in the Downloads folder is the
// ordinary mistake, and "altered or corrupted" is the wrong thing to say about
// it — it sends a person looking for an attacker rather than for the button.
func TestAnOlderDownloadIsNotReportedAsTampering(t *testing.T) {
	dir, file := signedRelease(t, "v1.1.0")
	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer server.Close()

	// This binary reports itself as v1.0.0 while the release says v1.1.0.
	check, err := verifySelf(context.Background(), server.Client(), server.URL+"/", file, "v1.0.0")
	if err != nil {
		t.Fatalf("verifySelfAgainst: %v", err)
	}
	if check.OK {
		t.Fatal("a v1.0.0 file was accepted as the v1.1.0 release")
	}
	if strings.Contains(check.Reason, "altered") || strings.Contains(check.Reason, "corrupted") {
		t.Errorf("an out-of-date download was reported as tampering:\n%s", check.Reason)
	}
	if !strings.Contains(check.Reason, "v1.1.0") {
		t.Errorf("the reason does not name the version to download:\n%s", check.Reason)
	}
}

// signedRelease writes a release folder signed by a throwaway key, points this
// build's PublicKey at it, and returns the folder and the binary inside it.
func signedRelease(t *testing.T, version string) (dir, binary string) {
	t.Helper()

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	previous := release.PublicKey
	release.PublicKey = base64.StdEncoding.EncodeToString(public)
	t.Cleanup(func() { release.PublicKey = previous })

	dir = t.TempDir()
	name := "BokkieSetup.exe"
	binary = filepath.Join(dir, name)
	if err := os.WriteFile(binary, []byte("the bytes of a bokkie release"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	info, err := os.Stat(binary)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := release.SHA256File(binary)
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.MarshalIndent(release.Manifest{
		Version:   version,
		Released:  "2026-01-01T00:00:00Z",
		Artifacts: []release.Artifact{{Platform: release.Platform(), File: name, Size: info.Size(), SHA256: sum}},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(filepath.Join(dir, release.ManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(private, data)) + "\n"
	if err := os.WriteFile(filepath.Join(dir, release.SignatureName), []byte(signature), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, binary
}

// A console's stdin does not reach EOF when somebody presses Enter, so an
// address prompt that reads to EOF never returns. This is the shape of a real
// console: a reader that stays open after a line is written.
func TestAskAddressReturnsWhenTheLineEnds(t *testing.T) {
	cases := map[string]struct {
		typed string
		want  string
	}{
		"Enter alone takes the default": {"\n", winsetup.DefaultAddress},
		"a typed address is used":       {"http://192.168.1.50:8080\n", "http://192.168.1.50:8080"},
		"trailing spaces are trimmed":   {"  http://192.168.1.50:8080  \n", "http://192.168.1.50:8080"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			reader, writer := io.Pipe()
			go func() {
				writer.Write([]byte(c.typed))
				// Deliberately never closed: a console does not close when the
				// person presses Enter, and that is the whole bug.
			}()

			type result struct {
				address string
				err     error
			}
			done := make(chan result, 1)
			go func() {
				address, err := askAddress(io.Discard, reader)
				done <- result{address, err}
			}()

			select {
			case got := <-done:
				if got.err != nil {
					t.Fatalf("askAddress: %v", got.err)
				}
				if got.address != c.want {
					t.Errorf("address = %q, want %q", got.address, c.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("askAddress never returned after the line ended; the setup window would hang forever on a counter PC")
			}
		})
	}
}

// Offering both stop and start would offer one that does nothing, to a person
// who is being asked to tell the developer what they can see.
func TestTheMenuOffersStopOrStartButNeverBoth(t *testing.T) {
	running := actions(menuFor(setupState{Installed: true, InstalledVersion: "v1.0.0", ThisVersion: "v1.0.0"}))
	if !contains(running, actionStop) {
		t.Error("a running station was not offered a way to stop it")
	}
	if contains(running, actionStart) {
		t.Error("a running station was offered a start")
	}

	silent := actions(menuFor(setupState{Installed: true, ThisVersion: "v1.0.0"}))
	if !contains(silent, actionStart) {
		t.Error("an installed station that is not answering was not offered a start")
	}
	if contains(silent, actionStop) {
		t.Error("a station that is not answering was offered a stop")
	}
}

// The moment to run the check is when something is wrong, and an administrator
// prompt in the way is one more thing between the person and the answer.
func TestTheCheckIsAlwaysOfferedAndNeverNeedsAPassword(t *testing.T) {
	for _, state := range []setupState{
		{ThisVersion: "v1.0.0"},
		{Installed: true, InstalledVersion: "v1.0.0", ThisVersion: "v1.0.0"},
	} {
		if !contains(actions(menuFor(state)), actionDoctor) {
			t.Errorf("state %+v was not offered the check", state)
		}
	}
	if needsAdmin(actionDoctor) {
		t.Error("the check asks for administrator rights; it changes nothing and must not")
	}
}

func contains(haystack []setupAction, needle setupAction) bool {
	for _, a := range haystack {
		if a == needle {
			return true
		}
	}
	return false
}
