package winsetup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// The success lines, one per thing this window can do. They are here rather
// than at the call sites so the voice stays one voice.
const (
	DoneInstalled = "Bokkie is installed and starts with this computer.\n\n" +
		"Open this address on any computer or phone at the station:\n" +
		"    http://bokkie.local:8080"
	DoneUpdated = "Bokkie has been updated. The database was backed up first.\n\n" +
		"Open http://bokkie.local:8080 and check the version on the home page."
	DoneShortcut = "A \"Bokkie\" shortcut is now on this computer's desktop.\n\n" +
		"If opening it says the page cannot be reached, the host computer is\n" +
		"switched off, or it is not on this network."
	DoneRemoved = "Bokkie has been removed from this computer.\n\n" +
		"The data folder and every backup in it have been left exactly where\n" +
		"they were. Nothing has been deleted."
)

// ErrPartial marks a failure that happened after this computer had already
// started being changed.
//
// It exists because the ordinary failure message promises the old system is
// untouched, and service.Install deregisters the existing service before it
// puts the new binary in place. A failure after that point leaves a station
// with no registered service, and telling its owner nothing was touched is the
// one sentence that would have sent them looking.
var ErrPartial = errors.New("this computer had already started being changed when it stopped")

// errVerdict marks a message that is news rather than a fault — an
// out-of-date download, say. The file was checked; the check simply had an
// answer. Wrapping it in "this did not finish, send the developer everything
// above" buries the one sentence the person can act on.
var errVerdict = errors.New("verdict")

// Verdict returns an error carrying a message the window should print plainly.
func Verdict(message string) error { return fmt.Errorf("%s%w", message, wrap(errVerdict)) }

// Partial marks err as having happened mid-change.
func Partial(err error) error { return fmt.Errorf("%w: %w", ErrPartial, err) }

// wrap hides a sentinel inside a message without printing it. errors.Is still
// finds it; the person never reads the word "verdict".
func wrap(sentinel error) error { return silent{sentinel} }

type silent struct{ error }

func (s silent) Error() string { return "" }
func (s silent) Unwrap() error { return s.error }

// PressEnterToClose holds the window open.
//
// Without it a double-click is a console that flashes and vanishes, taking the
// only copy of what went wrong with it — which is the open item step 2.5 named
// "the operator's first inch".
func PressEnterToClose(w io.Writer, r io.Reader) {
	fmt.Fprint(w, "\nPress Enter to close this window.")
	bufio.NewReader(r).ReadString('\n')
}

// ExitMessage is what the window says last.
//
// A cancelled UAC prompt is not a fault and must not read like one; anything
// else gets the words Install Bokkie.cmd uses, because what the person does
// next is the same either way.
//
// done is the caller's success line, because this window does four different
// things and only one of them installs anything. Telling somebody who asked for
// a desktop shortcut that Bokkie now starts with this computer would be a lie
// about the most consequential thing the file can do.
func ExitMessage(err error, done string) string {
	switch {
	case err == nil:
		return done

	// Neither of these is a fault, and both read as one if they go through the
	// branch below. A person who chose not to continue, and a person holding
	// an out-of-date file, each need one sentence and no alarm.
	case errors.Is(err, ErrElevationCancelled), errors.Is(err, errVerdict):
		return err.Error()

	case errors.Is(err, ErrPartial):
		return "This did not finish, and this computer had already started being\n" +
			"changed when it stopped. Bokkie may be part-installed or part-removed.\n\n" +
			"Do not run this again until the developer has seen this screen.\n\n" +
			"Send the developer everything printed above.\n\n" +
			err.Error()

	default:
		return "This did not finish. Nothing has been removed and the old\n" +
			"system, if there was one, is untouched.\n\n" +
			"Send the developer everything printed above.\n\n" +
			err.Error()
	}
}
