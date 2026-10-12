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
	case errors.Is(err, ErrElevationCancelled):
		return err.Error()
	default:
		return "This did not finish. Nothing has been removed and the old\n" +
			"system, if there was one, is untouched.\n\n" +
			"Send the developer everything printed above.\n\n" +
			err.Error()
	}
}
