package logging

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDailyWriterRotatesAndPrunes(t *testing.T) {
	dir := t.TempDir()
	w, err := newDailyWriter(dir, 3)
	if err != nil {
		t.Fatalf("newDailyWriter: %v", err)
	}
	defer w.Close()

	day := time.Date(2026, time.September, 15, 8, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return day }

	// Five consecutive days of logging, retaining three.
	for i := 0; i < 5; i++ {
		day = day.AddDate(0, 0, 1)
		if _, err := w.Write([]byte("line\n")); err != nil {
			t.Fatalf("write on day %d: %v", i, err)
		}
	}

	want := []string{
		"bokkie-2026-09-18.log",
		"bokkie-2026-09-19.log",
		"bokkie-2026-09-20.log",
	}
	if got := logFiles(t, dir); !equal(got, want) {
		t.Errorf("log files = %v, want %v", got, want)
	}
}

func TestDailyWriterAppendsWithinTheSameDay(t *testing.T) {
	dir := t.TempDir()
	w, err := newDailyWriter(dir, 2)
	if err != nil {
		t.Fatalf("newDailyWriter: %v", err)
	}
	fixed := time.Date(2026, time.September, 15, 8, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return fixed }

	if _, err := w.Write([]byte("morning\n")); err != nil {
		t.Fatal(err)
	}
	// A restart mid-day must continue the file, not truncate the morning.
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("afternoon\n")); err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	body, err := os.ReadFile(filepath.Join(dir, "bokkie-2026-09-15.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "morning\nafternoon\n" {
		t.Errorf("log contents = %q, want both lines", body)
	}
}

func TestNewRejectsZeroRetention(t *testing.T) {
	if _, _, err := New(Options{Dir: t.TempDir(), Retention: 0}); err == nil {
		t.Fatal("New succeeded with retention 0, want an error")
	}
}

func TestParseLevel(t *testing.T) {
	if _, err := ParseLevel("WARN"); err != nil {
		t.Errorf("ParseLevel(WARN): %v", err)
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Error("ParseLevel(verbose) succeeded, want an error — a typo'd level must not silently log nothing")
	}
}

func logFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
