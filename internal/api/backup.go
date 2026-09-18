package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/backup"
)

// backupFileView is one file in the backup folder.
//
// Deliberately says nothing about whether the file is *valid*. Every backup is
// verified at the moment it is written, but that was then — a stick can be
// pulled mid-copy, and a list that printed "verified" beside a file it had not
// opened would be asserting something it does not know. Checking one is
// `bokkie backup -verify`, which reads the file.
type backupFileView struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes,omitempty"`

	// TakenAt is when the backup was written, in station time, read from the
	// file's own name rather than from its modification time.
	//
	// The name is UTC because it has to sort lexicographically in a directory
	// listing whatever locale the host is in. Showing that raw beside a local
	// clock is eight hours of confusion in this timezone — "which one is last
	// night's?" — so it is converted here, in the one place that knows the
	// station's timezone. Modification time is not used at all: for a file
	// copied off a USB stick and back it records the copy, not the backup.
	TakenAt string `json:"taken_at,omitempty"`
}

// handleBackupNow runs a backup on demand.
//
// Manager-only, and registered as a non-transactional mutation because SQLite
// refuses VACUUM INTO inside a transaction — see routes.go for why that is a
// separate registration path rather than a special case.
func handleBackupNow(d Deps) NonTxHandler {
	return func(ctx context.Context, r *http.Request) (Change, error) {
		result, err := d.Backup.RunNow(ctx)
		if err != nil {
			// The cause is almost always the destination being unavailable —
			// the USB stick is not plugged in — so the message names the
			// folder rather than saying "backup failed".
			return Change{}, statusError{
				status:  http.StatusServiceUnavailable,
				message: "the backup could not be written or verified: " + err.Error(),
			}
		}

		return Change{
			EntityID: result.Path,
			After: map[string]any{
				"path":       result.Path,
				"bytes":      result.Bytes,
				"took_ms":    result.Took.Milliseconds(),
				"row_counts": result.RowCounts,
				"pruned":     len(result.Pruned),
			},
			Body: map[string]any{
				"path":       result.Path,
				"bytes":      result.Bytes,
				"took_ms":    result.Took.Milliseconds(),
				"row_counts": result.RowCounts,
			},
		}, nil
	}
}

// handleListBackups shows what is in the backup folder. Manager-only, and read
// only: it is how someone checks the nightly job is actually producing files
// without logging into the host.
func handleListBackups(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		files, err := backup.List(d.BackupFolder)
		if err != nil {
			d.Logger.Error("list backups", "id", RequestID(r.Context()), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(r.Context())})
			return
		}
		// A folder with no backups in it yet is an empty list, never a null.
		// Go marshals a nil slice as `null`, and a screen that reads .length
		// off one takes the whole component down — which is how step 7's
		// interest run with nothing to charge produced a blank page.
		views := make([]backupFileView, 0, len(files))
		for _, path := range files {
			view := backupFileView{Name: filepath.Base(path), Path: path}
			// A file that cannot be stat-ed is still listed, with no size. It
			// is on the USB stick and somebody needs to know it is there; a
			// size of zero would be a claim, and an omitted one is not.
			if info, err := os.Stat(path); err == nil {
				view.Bytes = info.Size()
			}
			if at, ok := backup.TakenAt(path); ok {
				view.TakenAt = at.In(d.Settings.Location()).Format(time.RFC3339)
			}
			views = append(views, view)
		}

		// Newest first: the question this list answers is "did last night's
		// backup run", and backup.List sorts oldest first for its own reasons.
		slices.Reverse(views)

		writeJSON(w, http.StatusOK, map[string]any{
			"folder":  d.BackupFolder,
			"backups": views,
		})
	}
}
