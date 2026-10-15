package winsetup

import (
	"strings"
	"testing"
)

// The half that matters is /T. The records were written before this account
// existed as far as that folder is concerned, so a grant that only inherits to
// new files leaves bokkie.db exactly as unreadable as it was.
func TestGrantReachesWhatIsAlreadyThere(t *testing.T) {
	args := GrantArgs(`C:\ProgramData\Bokkie`, `STATION\tls`)

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "/T") {
		t.Error("no /T: the grant would not reach the records already in the folder")
	}
	if !strings.Contains(joined, "(OI)(CI)") {
		t.Error("no (OI)(CI): the grant would not inherit to files written later")
	}
	if args[0] != `C:\ProgramData\Bokkie` {
		t.Errorf("the folder is not the first argument: %v", args)
	}
	if !strings.Contains(joined, `STATION\tls:(OI)(CI)M`) {
		t.Errorf("the account is not granted modify rights: %v", args)
	}
}

// icacls takes the folder as one argument, so a path with a space in it — and
// every path this will ever see has one — must not be split across two.
func TestTheFolderStaysOneArgument(t *testing.T) {
	args := GrantArgs(`C:\Program Files\Bokkie`, "tls")
	if len(args) != 4 {
		t.Fatalf("GrantArgs produced %d arguments, want 4: %v", len(args), args)
	}
	if args[0] != `C:\Program Files\Bokkie` {
		t.Errorf("the folder was mangled: %q", args[0])
	}
}
