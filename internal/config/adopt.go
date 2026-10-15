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

// Writable reports whether this process can actually use dir as a data folder,
// by trying the two things SQLite will need: a new file in the folder, and a
// write to the database already in it.
//
// Both halves are necessary, and the second one is the half that was missing.
// On Windows, C:\ProgramData lets a standard user create new files by
// inheritance — so a probe that only created one passed — while bokkie.db,
// written by an elevated install of an older version and granted to the
// service account that ran it, stayed read-only to the person who now opens
// the window. The station then got as far as opening the database and failed
// on the first write with "attempt to write a readonly database", naming no
// folder and no cause.
//
// Asking is the only way to know. Inspecting the ACL to work it out would be
// reimplementing the answer the filesystem gives for free.
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
	if err := os.Remove(probe); err != nil {
		return err
	}

	// The database itself, if there is one. Opened for writing and closed
	// without writing anything: the open is the whole question.
	database := filepath.Join(dir, DatabaseName)
	if _, err := os.Stat(database); err != nil {
		return nil
	}
	existing, err := os.OpenFile(database, os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("the records file %s cannot be written to: %w", database, err)
	}
	return existing.Close()
}

// DatabaseName is the file every data folder is identified by. It is a
// constant here because the folder has to be chosen before there is a Config
// to ask.
const DatabaseName = "bokkie.db"

// ErrDataFolderUnwritable marks the upgraded station that cannot open its own
// ledger.
//
// It is a sentinel so the window can tell this apart from a fault. "This did
// not finish, send the developer everything printed above" is the wrong thing
// to say about a problem whose fix is one command the person can run — and it
// is what buries that command under two paragraphs telling them to phone.
var ErrDataFolderUnwritable = errors.New("the data folder cannot be written to")

// UnwritableError is the upgraded station that cannot open its own ledger, and
// the folder it happened to.
//
// Typed, rather than a formatted string, because the window offers to fix it —
// and the thing doing the fixing needs the path, not a paragraph with the path
// somewhere inside it.
type UnwritableError struct {
	Dir   string
	Cause error
}

func (e *UnwritableError) Unwrap() error { return ErrDataFolderUnwritable }

func (e *UnwritableError) Error() string {
	return fmt.Sprintf("%s.\n\n"+
		"Bokkie found its records in %s but cannot write there.\n"+
		"\n"+
		"That folder was made by an older version of Bokkie, which ran as a\n"+
		"Windows service. This version runs as you, and that account has not\n"+
		"been given permission to it.\n"+
		"\n"+
		"Nothing has been changed and no records have been lost.\n"+
		"\n"+
		"Run Bokkie again and choose \"Let Bokkie fix this\" — it will ask for\n"+
		"permission once and then open normally.\n"+
		"\n"+
		"(%v)", ErrDataFolderUnwritable, e.Dir, e.Cause)
}

// UnwritableFolder returns the folder an error like this is about, and whether
// it was one.
func UnwritableFolder(err error) (string, bool) {
	var unwritable *UnwritableError
	if errors.As(err, &unwritable) {
		return unwritable.Dir, true
	}
	return "", false
}

// adoptedUnwritable is the upgraded station that cannot open its own ledger.
//
// It is its own error because it is the one failure this whole step must not
// produce: a folder holding a year of lending, found correctly, and then not
// openable by the person who double-clicked the program. The message says what
// to do rather than naming a log file nobody can act on.
func adoptedUnwritable(dir string, cause error) error {
	return &UnwritableError{Dir: dir, Cause: cause}
}
