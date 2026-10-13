package config

import (
	"errors"
	"fmt"
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
