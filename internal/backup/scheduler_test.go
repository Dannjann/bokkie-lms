package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// Review Focus 5. Closing the window asks for a backup, and the nightly job may
// be mid-VACUUM when it does. Two at once would leave a file that fails its own
// verification — worse than one fewer backup, because somebody will trust it.
//
// What is asserted is the folder, not a counter. Backups are named to the
// second, so several in one second cannot all produce a file, and `Run` refuses
// to overwrite one that exists. Both outcomes are safe; a half-written file is
// not, and that is what this looks for.
func TestConcurrentBackupsNeverLeaveAPartialFile(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	store, err := settings.Open(ctx, database)
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	folder := t.TempDir()
	scheduler := NewScheduler(database, store, folder, discardLogger())

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A refusal is a correct outcome here: the second-resolution name
			// is already taken. Anything else is not.
			if _, err := scheduler.RunNow(ctx); err != nil && !strings.Contains(err.Error(), "already exists") {
				t.Errorf("RunNow failed for a reason that is not a name collision: %v", err)
			}
		}()
	}
	wg.Wait()

	paths, err := List(folder)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("four backups ran and the folder is empty")
	}
	// List returns full paths, which is what Verify wants.
	for _, path := range paths {
		if _, err := Verify(ctx, path); err != nil {
			t.Errorf("%s does not verify; a backup was written over another: %v", path, err)
		}
	}
}

// A process killed mid-VACUUM used to leave a half-written file under the real
// name — which List returns, the screen shows as the last backup taken, and
// prune keeps while deleting a genuine older one to make room. Since 2.7 a
// backup runs as the window closes, which is exactly when somebody holds the
// power button.
func TestAHalfWrittenBackupIsNeverMistakenForOne(t *testing.T) {
	folder := t.TempDir()

	// What a killed process leaves behind: the partial name, with rubbish in it.
	partial := filepath.Join(folder, FilePrefix+"20261013-170000"+FileSuffix+partialSuffix)
	if err := os.WriteFile(partial, []byte("half a database"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := List(folder)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List returned %v; a half-written file was offered as a backup", got)
	}

	// And the next run clears it rather than leaving it to accumulate.
	ctx := context.Background()
	database := openTestDB(t)
	store, err := settings.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewScheduler(database, store, folder, discardLogger()).RunNow(ctx); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Errorf("the leftover partial is still there: %v", err)
	}
}

// The finished file must be whole the instant it carries the real name, which
// is what the rename buys.
func TestAFinishedBackupVerifies(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	store, err := settings.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()

	if _, err := NewScheduler(database, store, folder, discardLogger()).RunNow(ctx); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	paths, err := List(folder)
	if err != nil || len(paths) != 1 {
		t.Fatalf("List = %v, %v; want one backup", paths, err)
	}
	if _, err := Verify(ctx, paths[0]); err != nil {
		t.Errorf("the finished backup does not verify: %v", err)
	}
}
