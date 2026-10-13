package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Candidate is a data folder that might be the one holding the ledger.
type Candidate struct {
	Path        string
	HasDatabase bool
}

// ChooseDataDir picks the folder to use.
//
// The rule is "follow the data". The preferred location is first in the list,
// but a folder with a database in it beats an empty preferred one every time: a
// newer binary that silently opens an empty ledger on a station with real money
// in it is the worst outcome this system has, and it must not be reachable by
// double-clicking something.
//
// Two folders with databases is not a choice to make quietly. It is refused,
// both are named, and it is left to a person — in a system whose whole job is
// being able to say what happened, picking one of two ledgers automatically is
// exactly the wrong instinct.
func ChooseDataDir(candidates []Candidate) (string, error) {
	var withData []Candidate
	for _, c := range candidates {
		if c.HasDatabase {
			withData = append(withData, c)
		}
	}

	switch len(withData) {
	case 0:
		if len(candidates) == 0 {
			return "", errors.New("no data folder to choose from")
		}
		return candidates[0].Path, nil
	case 1:
		return withData[0].Path, nil
	default:
		return "", fmt.Errorf(
			"two folders on this computer hold a Bokkie database, and picking one of them "+
				"automatically could hide a year of records:\n  %s\n  %s\n"+
				"Move or rename the one that is not in use, then start Bokkie again",
			withData[0].Path, withData[1].Path)
	}
}

// Writable reports whether this process can actually create files in dir, by
// creating one and removing it.
//
// Asking is the only way to know. On Windows the folder an older version made
// was created by an elevated install and granted to the service account that
// ran it; the interactive user who now opens the window may have read access
// and nothing more. Inspecting the ACL to work that out would be reimplementing
// the answer the filesystem will give for free.
func Writable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	probe := filepath.Join(dir, ".bokkie-write-test")
	file, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	file.Close()
	return os.Remove(probe)
}

// ErrAdoptedUnwritable is the upgraded station that cannot open its own ledger.
//
// It is its own error because it is the one failure this whole step must not
// produce: a folder holding a year of lending, found correctly, and then not
// openable by the person who double-clicked the program. The message says what
// to do rather than naming a log file nobody can act on.
func adoptedUnwritable(dir string, cause error) error {
	return fmt.Errorf("Bokkie found its records in %s but cannot write there.\n"+
		"\n"+
		"That folder was made by an older version of Bokkie, which ran as a\n"+
		"Windows service. This version runs as you, and has not been given\n"+
		"permission to it.\n"+
		"\n"+
		"Nothing has been changed and no records have been lost. Either:\n"+
		"  - right-click Bokkie and choose \"Run as administrator\" once, or\n"+
		"  - ask the developer to move that folder.\n"+
		"\n"+
		"(%w)", dir, cause)
}
