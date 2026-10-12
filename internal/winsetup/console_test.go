package winsetup

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Review Focus 1. A dismissed UAC prompt is the most common way this ends, and
// reading "send the developer everything printed above" after deciding not to
// continue is how a person concludes the software is broken.
func TestExitMessageDoesNotTreatACancelledPromptAsAFault(t *testing.T) {
	got := ExitMessage(fmt.Errorf("install: %w", ErrElevationCancelled), "")
	if strings.Contains(got, "Send the developer") {
		t.Errorf("a cancelled prompt was reported as a fault:\n%s", got)
	}
	if !strings.Contains(got, "nothing has been changed") {
		t.Errorf("a cancelled prompt did not say the machine is untouched:\n%s", got)
	}
}

// The window does four things and only one of them installs anything.
func TestExitMessageSaysWhatWasActuallyDone(t *testing.T) {
	got := ExitMessage(nil, DoneShortcut)
	if strings.Contains(got, "starts with this computer") {
		t.Errorf("a shortcut-only run claimed Bokkie had been installed:\n%s", got)
	}
	if !strings.Contains(ExitMessage(nil, DoneInstalled), "bokkie.local") {
		t.Error("the install message does not say where to open the app")
	}
}

func TestExitMessageOnARealFailureSaysWhatIsUntouchedAndWhatToSend(t *testing.T) {
	got := ExitMessage(errors.New("register the bokkie service: access is denied"), "")
	for _, want := range []string{"untouched", "Send the developer", "access is denied"} {
		if !strings.Contains(got, want) {
			t.Errorf("the failure message is missing %q:\n%s", want, got)
		}
	}
}

func TestElevatedArgsNameTheActionSoTheChildCannotRepeatTheMenu(t *testing.T) {
	got := ElevatedArgs("install")
	want := []string{"setup", "--elevated", "--do=install"}
	if len(got) != len(want) {
		t.Fatalf("ElevatedArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ElevatedArgs = %v, want %v", got, want)
		}
	}
}

// service.Install deregisters the existing service before it replaces the
// binary, so a failure after that point leaves a station with no service at
// all. Telling its owner the old system is untouched is the one sentence that
// would have sent them looking.
func TestExitMessageDoesNotPromiseAnUntouchedMachineAfterItWasTouched(t *testing.T) {
	got := ExitMessage(Partial(errors.New("start the bokkie service: access is denied")), "")
	if strings.Contains(got, "untouched") {
		t.Errorf("a failure mid-change claimed the old system is untouched:\n%s", got)
	}
	for _, want := range []string{"part-installed", "Do not run this again", "access is denied"} {
		if !strings.Contains(got, want) {
			t.Errorf("the partial-failure message is missing %q:\n%s", want, got)
		}
	}
}

// An out-of-date download is news, not a fault. Wrapping it in "this did not
// finish, send the developer everything above" buries the one sentence the
// person can act on under two that send them to the phone instead.
func TestExitMessagePrintsAVerdictPlainly(t *testing.T) {
	const reason = "This file is Bokkie v1.0.0, and the current release is v1.1.0."
	got := ExitMessage(Verdict(reason), "")
	if got != reason {
		t.Errorf("ExitMessage = %q, want exactly the verdict", got)
	}
	if strings.Contains(got, "Send the developer") {
		t.Errorf("a verdict was reported as a fault:\n%s", got)
	}
}
