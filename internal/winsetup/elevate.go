package winsetup

import "errors"

// ErrElevationCancelled is the UAC prompt being dismissed. It is a sentence
// rather than a failure code because the person who dismissed it is the person
// who will read it.
var ErrElevationCancelled = errors.New("Windows was not given permission to continue, so nothing has been changed on this computer")

// ElevatedArgs are the arguments the elevated copy of ourselves is started
// with.
//
// They are explicit rather than empty on purpose: a child relaunched bare would
// run the same double-click detection its parent did, decide it had been
// double-clicked, and have no way to know it was already the second attempt.
// Naming the action also means the elevated window does one thing and closes,
// instead of showing the menu twice.
func ElevatedArgs(action string) []string {
	return []string{"setup", "--elevated", "--do=" + action}
}
