package config

import (
	"errors"
	"os"
	"path/filepath"
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

// The upgraded station's worst case: its ledger is found correctly, in a
// folder made by an elevated install of an older version, and the person who
// double-clicked the program cannot write there. Without the probe the first
// sign of it is "cannot open bokkie-2026-10-13.log", which names the wrong
// problem entirely.
func TestAnUnwritableFolderIsNoticedAndExplained(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; every folder is writable")
	}

	dir := t.TempDir()
	locked := filepath.Join(dir, "Bokkie")
	if err := os.Mkdir(locked, 0o500); err != nil { // read and traverse, no write
		t.Fatal(err)
	}

	err := Writable(locked)
	if err == nil {
		t.Fatal("a folder with no write permission was reported writable")
	}

	message := adoptedUnwritable(locked, err).Error()
	for _, want := range []string{locked, "no records have been lost", "Run as administrator"} {
		if !strings.Contains(message, want) {
			t.Errorf("the message is missing %q:\n%s", want, message)
		}
	}
}

func TestAWritableFolderPasses(t *testing.T) {
	if err := Writable(filepath.Join(t.TempDir(), "Bokkie")); err != nil {
		t.Errorf("a fresh folder was reported unwritable: %v", err)
	}
}

// The station's actual failure, and the hole the first probe had. On Windows a
// standard user can usually create a new file in C:\ProgramData by
// inheritance, so a folder-only probe passed — while bokkie.db, written by an
// elevated install of an older version, stayed read-only. The station got as
// far as opening the database and died on the first write with "attempt to
// write a readonly database", naming no folder and no cause.
func TestAWritableFolderHoldingAReadOnlyDatabaseIsNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; every file is writable")
	}

	dir := t.TempDir()
	database := filepath.Join(dir, DatabaseName)
	if err := os.WriteFile(database, []byte("a ledger"), 0o400); err != nil { // read-only
		t.Fatal(err)
	}

	// The folder itself takes new files, which is exactly what made this
	// invisible.
	probe := filepath.Join(dir, "proof-the-folder-is-writable")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatalf("the test's premise is wrong — the folder is not writable: %v", err)
	}
	os.Remove(probe)

	err := Writable(dir)
	if err == nil {
		t.Fatal("a folder whose database cannot be written was reported writable; the station would die on its first write")
	}
	if !strings.Contains(err.Error(), DatabaseName) {
		t.Errorf("the error does not name the records file: %v", err)
	}

	// And the message a person reads names the folder and the one command.
	message := adoptedUnwritable(dir, err).Error()
	for _, want := range []string{dir, "icacls", "no records have been lost"} {
		if !strings.Contains(message, want) {
			t.Errorf("the message is missing %q:\n%s", want, message)
		}
	}
}

// A folder with a database this user can write is the ordinary case and must
// not be refused.
func TestAWritableDatabasePasses(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DatabaseName), []byte("a ledger"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Writable(dir); err != nil {
		t.Errorf("a writable ledger was reported unwritable: %v", err)
	}
}

// The window tells a verdict apart from a fault by this sentinel. Without it
// the one command that fixes this is printed under two paragraphs telling the
// person to send the developer everything on the screen.
func TestTheUnwritableFolderCarriesItsSentinel(t *testing.T) {
	err := adoptedUnwritable(`C:\ProgramData\Bokkie`, errors.New("access is denied"))
	if !errors.Is(err, ErrDataFolderUnwritable) {
		t.Error("the error does not carry ErrDataFolderUnwritable; the window would report it as a fault")
	}
	if !strings.Contains(err.Error(), "access is denied") {
		t.Errorf("the cause was lost: %v", err)
	}
}
