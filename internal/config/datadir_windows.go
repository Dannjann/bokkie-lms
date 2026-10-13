//go:build windows

package config

import (
	"os"
	"path/filepath"
)

// databaseName is what a data folder holds when it is the one in use. It is
// repeated here rather than taken from Config, because this runs before there
// is a Config to take it from.
const databaseName = "bokkie.db"

// defaultDataDir is %LOCALAPPDATA%\Bokkie, or wherever the ledger actually is.
//
// It used to be C:\ProgramData\Bokkie, machine-wide, because the service ran as
// LocalService with nobody logged in. There is no service and no service
// account any more: the station runs while a person has its window open, so the
// folder belongs to that person and is always writable without a prompt.
//
// The old location is still offered as a candidate, and wins when it is the one
// holding the database. A station upgraded from 2.6 has a year of lending in
// C:\ProgramData\Bokkie, and a new binary that quietly opened an empty folder
// instead would be the worst thing this program could do.
func defaultDataDir() (string, error) {
	candidates := []Candidate{}
	for _, base := range []string{os.Getenv("LOCALAPPDATA"), os.Getenv("ProgramData"), `C:\ProgramData`} {
		if base == "" {
			continue
		}
		path := filepath.Join(base, "Bokkie")
		if alreadyListed(candidates, path) {
			continue
		}
		candidates = append(candidates, Candidate{Path: path, HasDatabase: hasDatabase(path)})
	}
	return ChooseDataDir(candidates)
}

func alreadyListed(candidates []Candidate, path string) bool {
	for _, c := range candidates {
		if c.Path == path {
			return true
		}
	}
	return false
}

func hasDatabase(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, databaseName))
	return err == nil && !info.IsDir()
}
