package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Dannjann/bokkie-lms/internal/buildinfo"
	"github.com/Dannjann/bokkie-lms/internal/release"
	"github.com/Dannjann/bokkie-lms/internal/service"
	"github.com/Dannjann/bokkie-lms/internal/winsetup"
)

// defaultPort is where the station listens unless the settings file says
// otherwise. It is repeated here because the window has to say something
// sensible even when that file cannot be read, which is the case the repair
// below exists for.
const defaultPort = 8080

// setupState is what the guided window knows about this machine before it
// offers anything.
type setupState struct {
	// Running is a station answering on the port right now, which since 2.7
	// means somebody has a window open. It is deliberately not called
	// "installed": nothing is installed any more, and a review found a menu
	// that read "answering" as "installed" and offered to repair it — which
	// would have registered an auto-starting service.
	Running        bool
	RunningVersion string
	ThisVersion    string
	Elevated       bool

	// RunningFromInstallPath is the person having double-clicked the copy in
	// Program Files rather than the one they downloaded.
	RunningFromInstallPath bool

	// OldServiceInstalled is a service left behind by a 2.6 install, which
	// starts the station on its own and must not.
	OldServiceInstalled bool

	// DataFolder is where the records are, and DataFolderUnwritable is this
	// computer being unable to write them — an upgrade from a version that ran
	// as a service, whose folder belongs to an account this one is not.
	DataFolder           string
	DataFolderUnwritable bool
}

type setupAction int

// The actions a 2.7 window can take. Install, repair, uninstall, stop and start
// are deliberately absent rather than merely unused: each registered or managed
// the Windows service this step abolished, and a constant that still exists is
// one somebody can reach through --do=.
//
// actionQuit is the zero value on purpose. A name this list does not know
// resolves to it, so a --do this build no longer understands closes the window
// rather than doing something nobody asked for.
const (
	actionQuit setupAction = iota
	actionShortcut
	actionServe
	actionDoctor
	actionUpdate
	actionRemoveService
	actionAlreadyRunning
	actionFixDataFolder
)

// setupChoice is one line of the menu.
type setupChoice struct {
	Action     setupAction
	Label      string
	NeedsAdmin bool
}

// adminActions are the choices that change this computer for everyone who uses
// it. The shortcut is deliberately absent: it writes one file to one desktop,
// and a counter PC is where it is usually run, by somebody who does not have an
// administrator password and should not need one.
// adminActions are the choices that change this computer for everyone who uses
// it. Starting the station is deliberately absent: it opens a window and binds
// a port, and asking a counter PC's user for an administrator password to do
// that is asking for one they do not have.
var adminActions = map[setupAction]bool{
	actionUpdate:        true,
	actionRemoveService: true,
	actionFixDataFolder: true,
}

// serviceActions are the names older builds used for the things that
// registered or managed a Windows service. None of them resolves to anything
// now; the list exists so a test can say so.
var serviceActions = []string{"install", "repair", "uninstall", "stop", "start"}

// needsAdmin is read both when the menu is built and when a choice is
// dispatched, so the prompt and the label cannot disagree.
func needsAdmin(a setupAction) bool { return adminActions[a] }

// menuFor decides what this machine is offered.
//
// Since 2.7 there is no install here at all. Every choice that registered a
// Windows service is gone — a review found two of them still reachable, one of
// which ("Repair this installation") was offered to a station that was merely
// *answering*, and would have registered an auto-starting service on a machine
// that had never had one. Bokkie is open when a person has opened its window
// and at no other time, and a menu that can undo that is the menu being wrong.
//
// The counter PCs are the reason the shortcut is offered first on a machine
// with nothing installed, and the reason it is marked as needing no
// administrator: most people running this file are standing at a counter PC
// that must never become a second host.
func menuFor(state setupState) []setupChoice {
	choice := func(a setupAction, label string) setupChoice {
		return setupChoice{Action: a, Label: label, NeedsAdmin: needsAdmin(a)}
	}
	shortcut := choice(actionShortcut, "Put a Bokkie shortcut on this computer's desktop")
	closeIt := choice(actionQuit, "Close")

	// Nothing else is worth offering while Bokkie cannot read its own records.
	// It will not start, so every other choice ends in the same failure, and a
	// menu of them is a menu of wrong turns.
	if state.DataFolderUnwritable {
		return []setupChoice{
			choice(actionFixDataFolder, "Let Bokkie fix this — give this computer permission to its own records"),
			choice(actionDoctor, "Check what is wrong (changes nothing)"),
			closeIt,
		}
	}

	choices := []setupChoice{shortcut}

	// A station that is answering already has a window open somewhere. Offering
	// to start a second one would bind a port that is taken and confuse
	// somebody who is looking for the window they already have.
	if state.Running {
		choices = append(choices, choice(actionAlreadyRunning,
			"Bokkie is already running on this computer, version "+state.RunningVersion))
	} else {
		choices = append(choices, choice(actionServe,
			"Start Bokkie here — this makes this computer the host (runs while this window is open)"))
	}

	choices = append(choices, choice(actionDoctor, "Check what is wrong (changes nothing)"))

	// The leftover from an older install starts the station without anybody
	// asking, which is the one thing this step exists to stop.
	if state.OldServiceInstalled {
		choices = append(choices, choice(actionRemoveService,
			"Remove the old Bokkie service (it starts on its own, and should not)"))
	}

	// Updating replaces this computer's copy of the program. It is offered only
	// when there is something to replace and this file is a different version.
	if state.Running && state.RunningVersion != "" && state.RunningVersion != state.ThisVersion {
		choices = append(choices, choice(actionUpdate,
			"Update Bokkie from "+state.RunningVersion+" to "+state.ThisVersion))
	}

	return append(choices, closeIt)
}

// describeInstallation works out what this machine already has.
//
// The serving version is preferred over the file on disk because it is the one
// answering on the port — a binary sitting at the install path that is not
// running is a failed update, not an installation. Falling back to "installed
// but not answering" keeps the repair offer available in exactly that case.
func describeInstallation(exePath, installedPath, serving string, servingErr error) setupState {
	state := setupState{
		ThisVersion:            buildinfo.Version,
		Elevated:               winsetup.IsElevated(),
		RunningFromInstallPath: sameFile(exePath, installedPath),
	}

	if servingErr == nil && serving != "" {
		state.Running, state.RunningVersion = true, serving
	}
	_ = installedPath
	return state
}

// sameFile compares paths case-insensitively after cleaning, which is what
// Windows means by the same file for this purpose.
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// The names the elevated window is given on its command line. They are spelled
// once, here, because the unelevated window writes them and the elevated one
// reads them, and a disagreement between the two would be a window that
// silently does the wrong thing.
var actionNames = map[setupAction]string{
	actionShortcut:      "shortcut",
	actionServe:         "serve",
	actionDoctor:        "doctor",
	actionUpdate:        "update",
	actionRemoveService: "remove-service",
	actionFixDataFolder: "fix-data-folder",
}

func nameOf(a setupAction) string { return actionNames[a] }

// actionFor is nameOf backwards. Anything unrecognised is actionQuit, which is
// also the zero value — so a name this build no longer knows, the install and
// repair of older versions included, closes the window instead of acting.
func actionFor(name string) setupAction {
	for action, known := range actionNames {
		if known == name {
			return action
		}
	}
	return actionQuit
}

// ask prints the menu and reads one number.
func ask(w io.Writer, r io.Reader, state setupState) (setupAction, error) {
	choices := menuFor(state)

	fmt.Fprintf(w, "\nBokkie %s\n\n", state.ThisVersion)
	if state.Running {
		fmt.Fprintf(w, "Bokkie is running on this computer, version %s.\n\n", state.RunningVersion)
	} else {
		fmt.Fprint(w, "Bokkie is not running on this computer.\n\n")
	}
	for i, choice := range choices {
		fmt.Fprintf(w, "  %d. %s\n", i+1, choice.Label)
	}
	fmt.Fprint(w, "\nType a number and press Enter: ")

	var pick int
	if _, err := fmt.Fscanln(r, &pick); err != nil || pick < 1 || pick > len(choices) {
		// Not a failure worth a stack of words: they can run the file again.
		return actionQuit, nil
	}
	return choices[pick-1].Action, nil
}

// askAddress offers the host address the shortcut should point at.
func askAddress(w io.Writer, r io.Reader) (string, error) {
	fmt.Fprintf(w, "\nThe shortcut will open %s\n", winsetup.DefaultAddress)
	fmt.Fprint(w, "Press Enter to use that, or type the host computer's address\n")
	fmt.Fprint(w, "(for example http://192.168.1.50:8080): ")

	// One line, not everything until EOF. A console's stdin does not reach EOF
	// when somebody presses Enter, so reading to EOF here is a window that
	// never comes back — on the first menu item a counter PC is offered.
	line, err := bufio.NewReader(io.LimitReader(r, 512)).ReadString('\n')
	_ = err // a line with no newline after it is still a line.
	typed := strings.TrimSpace(line)
	if typed == "" {
		return winsetup.DefaultAddress, nil
	}
	return typed, nil
}

// programEntry is how Bokkie describes itself to Add/Remove Programs.
func programEntry(installedPath string) winsetup.Program {
	return winsetup.Program{
		Name:            service.DisplayName,
		Version:         buildinfo.Version,
		Publisher:       "Bokkie",
		InstallLocation: filepath.Dir(installedPath),
		Icon:            installedPath,
		UninstallExe:    installedPath,
		// --from-control-panel, because Windows runs this with no console to
		// print into and no one watching a terminal.
		UninstallArgs: []string{"uninstall", "--from-control-panel"},
	}
}

// setupOptions reuses the front end install and uninstall share, so the guided
// window resolves the same data directory and port they do.
func setupOptions() (service.Options, *slog.Logger, io.Closer, error) {
	return serviceCommand("setup", nil)
}

// selfCheck is what this file turned out to be when it was weighed against a
// signed manifest.
type selfCheck struct {
	OK       bool
	Manifest release.Manifest
	// Reason is why not, in words for the person reading the window. Empty
	// when OK.
	Reason string
	// Stale is a file that is a genuine release, just not the current one.
	// Kept apart from a byte mismatch because they are different events: one
	// is somebody who kept the installer in their Downloads folder, the other
	// is a file claiming to be this release while not being it.
	Stale bool
}

// verifySelf weighs this executable's own bytes against the current release.
//
// It hashes the file it is running from, which is the only honest thing to
// weigh: a check of some other file says nothing about the code doing the
// checking.
//
// Everything it depends on is a parameter rather than a global — the server to
// ask, the file to weigh, the version to claim — because the behaviour worth
// testing here is what it says about a file that does not match, and none of
// those three can be arranged from outside otherwise.
func verifySelf(ctx context.Context, client *http.Client, base, exe, thisVersion string) (selfCheck, error) {
	manifest, err := release.Fetch(ctx, client, base)
	if err != nil {
		return selfCheck{}, err
	}

	// An older file is the ordinary mistake — somebody kept BokkieSetup.exe in
	// Downloads and ran it again a release later. Saying "altered or corrupted"
	// about it is both wrong and alarming, so the versions are compared before
	// the hashes, which is the only place that distinction still exists.
	if manifest.Version != thisVersion {
		return selfCheck{Manifest: manifest, Stale: true, Reason: fmt.Sprintf(
			"This file is Bokkie %s, and the current release is %s.\n"+
				"Download the current one from the Bokkie downloads page.",
			thisVersion, manifest.Version)}, nil
	}

	artifact, err := manifest.ForHost()
	if err != nil {
		return selfCheck{Manifest: manifest, Reason: err.Error()}, nil
	}
	if err := artifact.Verify(exe); err != nil {
		return selfCheck{Manifest: manifest, Reason: err.Error()}, nil
	}
	return selfCheck{OK: true, Manifest: manifest}, nil
}

// weighSelf decides what to do about this file before it is allowed to touch
// the machine. It is the one place that decision is made, so that install,
// repair and update cannot drift apart on it.
//
// Three different things can be wrong, and a person acts differently on each:
//
//   - The check could not run at all — no internet. Here the asymmetry applies:
//     a working station is not replaced on the strength of a file nobody could
//     verify, but a bare machine proceeds, because refusing would mean a
//     station with no internet cannot be installed at all, which is what the
//     USB fallback is for.
//   - The check ran and the file is an older release. That is news, not a
//     fault: somebody kept the installer in their Downloads folder. It will
//     not go over a working station, and on a bare machine it is allowed with
//     a word about it.
//   - The check ran and the bytes do not match the release they claim to be.
//     That is refused everywhere, including on a bare machine. Nothing else in
//     this program treats that as survivable and nor should this.
func weighSelf(installed bool, check selfCheck, failure error) (proceed bool, notice error) {
	switch {
	case check.OK:
		return true, nil

	case failure != nil:
		why := "\n\n" + failure.Error()
		if installed {
			return false, errors.New("This file could not be checked against the Bokkie release key, so it will not\n" +
				"replace the working system on this computer. Nothing has been changed.\n\n" +
				"Download it again from the Bokkie downloads page, or update from the USB stick." + why)
		}
		return true, winsetup.Verdict("This file could not be checked against the Bokkie release key — this computer\n" +
			"may have no internet connection. Installing anyway, because there is nothing\n" +
			"on this computer to lose.\n\n" +
			"If you did not get this file from the Bokkie downloads page, close this window." + why)

	case check.Stale:
		if installed {
			return false, winsetup.Verdict(check.Reason + "\n\nNothing has been changed on this computer.")
		}
		return true, winsetup.Verdict(check.Reason + "\n\nInstalling it anyway, because there is nothing on this computer yet.")

	default:
		return false, errors.New("This file does not match the Bokkie release it says it is, so it will not be\n" +
			"installed on this computer. Nothing has been changed.\n\n" +
			"Download it again from the Bokkie downloads page.\n\n" + check.Reason)
	}
}
