package logging

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	filePrefix = "bokkie-"
	fileSuffix = ".log"
	// The log can name a customer ID but never a customer; even so, it records
	// the shape of the station's business and is not world-readable.
	filePerm fs.FileMode = 0o600
	dirPerm  fs.FileMode = 0o700
)

// dailyWriter writes to one file per day and prunes the oldest files past the
// retention count. Daily files beat size-based rotation here: the question
// being asked of these logs is almost always "what happened on the day the
// operator is telling me about".
type dailyWriter struct {
	dir       string
	retention int
	now       func() time.Time // swapped out in tests

	mu   sync.Mutex
	file *os.File
	day  string
}

func newDailyWriter(dir string, retention int) (*dailyWriter, error) {
	if retention < 1 {
		return nil, fmt.Errorf("log retention is %d; at least one day must be kept", retention)
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("create log directory %s: %w", dir, err)
	}
	return &dailyWriter{dir: dir, retention: retention, now: time.Now}, nil
}

func (w *dailyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.rotateTo(w.now().Format("2006-01-02")); err != nil {
		return 0, err
	}
	return w.file.Write(p)
}

func (w *dailyWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file, w.day = nil, ""
	return err
}

// rotateTo opens the file for the given day if it is not already open. Opening
// is append-only, so a restart mid-day continues the same file rather than
// truncating the morning's history.
func (w *dailyWriter) rotateTo(day string) error {
	if w.file != nil && w.day == day {
		return nil
	}
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}

	path := filepath.Join(w.dir, filePrefix+day+fileSuffix)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filePerm)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", path, err)
	}
	w.file, w.day = f, day

	// A failed prune must not stop the station logging; the disk cost of one
	// extra day is nothing next to a server that will not boot.
	_ = w.prune()
	return nil
}

// prune deletes the oldest daily files beyond the retention count. The names
// sort chronologically because the date is written as YYYY-MM-DD.
func (w *dailyWriter) prune() error {
	entries, err := filepath.Glob(filepath.Join(w.dir, filePrefix+"*"+fileSuffix))
	if err != nil {
		return err
	}
	if len(entries) <= w.retention {
		return nil
	}

	sort.Strings(entries)
	for _, path := range entries[:len(entries)-w.retention] {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
