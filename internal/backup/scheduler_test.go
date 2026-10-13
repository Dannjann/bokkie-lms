package backup

import (
	"context"
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
