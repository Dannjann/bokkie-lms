package winsetup

import "fmt"

// GrantArgs are the icacls arguments that give an account full control of a
// folder and everything already in it.
//
// Pure, so the one line that changes a permission on a station's records can be
// read and tested without a Windows machine. (OI) and (CI) make the grant
// inherit to files and folders created later; /T applies it to what is already
// there, which is the half that matters — the records were written before this
// account existed as far as that folder is concerned.
func GrantArgs(dir, account string) []string {
	return []string{dir, "/grant", account + ":(OI)(CI)M", "/T"}
}

// GrantFailed explains a grant that did not take.
func GrantFailed(dir string, cause error) error {
	return fmt.Errorf("could not give this account permission to %s: %w.\n"+
		"Nothing has been changed. Send the developer this message", dir, cause)
}
