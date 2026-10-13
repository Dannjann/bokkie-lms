package config

import (
	"strings"
	"testing"
)

// The upgrade case: the data is where the old version put it, and the new
// version prefers somewhere else. Following the preference would open an empty
// ledger on a station with a year of lending in it.
func TestTheEmptyPreferredFolderDoesNotWin(t *testing.T) {
	got, err := ChooseDataDir([]Candidate{
		{Path: `C:\Users\tls\AppData\Local\Bokkie`, HasDatabase: false},
		{Path: `C:\ProgramData\Bokkie`, HasDatabase: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != `C:\ProgramData\Bokkie` {
		t.Errorf("chose %q; a station with a ledger would have opened an empty one", got)
	}
}

func TestThePreferredFolderWinsWhenNothingHasData(t *testing.T) {
	got, err := ChooseDataDir([]Candidate{
		{Path: `C:\Users\tls\AppData\Local\Bokkie`},
		{Path: `C:\ProgramData\Bokkie`},
	})
	if err != nil || got != `C:\Users\tls\AppData\Local\Bokkie` {
		t.Errorf("got %q, %v; a fresh install belongs in the preferred folder", got, err)
	}
}

// Review Focus 4. Choosing between two real ledgers is not a decision to make
// quietly, in a system whose whole job is being able to say what happened.
func TestTwoLedgersAreRefusedRatherThanGuessedBetween(t *testing.T) {
	_, err := ChooseDataDir([]Candidate{
		{Path: `C:\Users\tls\AppData\Local\Bokkie`, HasDatabase: true},
		{Path: `C:\ProgramData\Bokkie`, HasDatabase: true},
	})
	if err == nil {
		t.Fatal("two databases were silently reduced to one")
	}
	for _, want := range []string{"ProgramData", "AppData", "Move or rename"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is missing %q: %v", want, err)
		}
	}
}

func TestNoCandidatesIsAnError(t *testing.T) {
	if _, err := ChooseDataDir(nil); err == nil {
		t.Error("an empty candidate list produced a path")
	}
}
