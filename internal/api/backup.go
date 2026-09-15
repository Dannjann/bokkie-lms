package api

import (
	"context"
	"net/http"

	"github.com/Dannjann/bokkie-lms/internal/backup"
)

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
		writeJSON(w, http.StatusOK, map[string]any{
			"folder":  d.BackupFolder,
			"backups": files,
		})
	}
}
