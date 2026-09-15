package service

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReplaceFilePutsTheSourceAtTheTarget(t *testing.T) {
	dir := t.TempDir()
	source := writeFile(t, filepath.Join(dir, "source"), "new")
	target := filepath.Join(dir, "nested", "target")

	displaced, err := replaceFile(target, source)
	if err != nil {
		t.Fatalf("replaceFile: %v", err)
	}
	if displaced != "" {
		t.Errorf("displaced = %q, want empty when nothing was there", displaced)
	}
	if got := readFile(t, target); got != "new" {
		t.Errorf("target contains %q, want %q", got, "new")
	}
	if _, err := os.Stat(source); err != nil {
		t.Errorf("the source was not left in place: %v", err)
	}
}

// The displaced file is what an update rolls back to, so replaceFile has to
// report where it went rather than quietly deleting it.
func TestReplaceFileKeepsWhatWasThereBefore(t *testing.T) {
	dir := t.TempDir()
	source := writeFile(t, filepath.Join(dir, "source"), "new")
	target := writeFile(t, filepath.Join(dir, "target"), "old")

	displaced, err := replaceFile(target, source)
	if err != nil {
		t.Fatalf("replaceFile: %v", err)
	}
	if displaced == "" {
		t.Fatal("displaced is empty, want the path the previous file was moved to")
	}
	if got := readFile(t, target); got != "new" {
		t.Errorf("target contains %q, want %q", got, "new")
	}
	if got := readFile(t, displaced); got != "old" {
		t.Errorf("displaced file contains %q, want %q", got, "old")
	}
}

// An interrupted earlier run leaves a .old behind. Windows refuses to rename
// onto an existing name, so a leftover would break every later install.
func TestReplaceFileOverwritesALeftoverDisplacedFile(t *testing.T) {
	dir := t.TempDir()
	source := writeFile(t, filepath.Join(dir, "source"), "new")
	target := writeFile(t, filepath.Join(dir, "target"), "old")
	writeFile(t, target+displacedSuffix, "stale")

	displaced, err := replaceFile(target, source)
	if err != nil {
		t.Fatalf("replaceFile: %v", err)
	}
	if got := readFile(t, displaced); got != "old" {
		t.Errorf("displaced file contains %q, want the file that was just replaced", got)
	}
}

func TestReplaceFileIsExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits do not decide executability on Windows")
	}
	dir := t.TempDir()
	// 0600: the mode the copy must not inherit.
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	target := filepath.Join(dir, "target")
	if _, err := replaceFile(target, source); err != nil {
		t.Fatalf("replaceFile: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("target mode is %v, want an executable one", info.Mode())
	}
}

// A service manager pointed at a name with nothing under it is the worst
// outcome, so a failure must leave the existing binary reachable.
func TestReplaceFileLeavesTheTargetAloneWhenTheSourceIsMissing(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, filepath.Join(dir, "target"), "old")

	if _, err := replaceFile(target, filepath.Join(dir, "does-not-exist")); err == nil {
		t.Fatal("replaceFile succeeded with no source file, want an error")
	}
	if got := readFile(t, target); got != "old" {
		t.Errorf("target contains %q, want the untouched %q", got, "old")
	}
	if _, err := os.Stat(target + stagedSuffix); !os.IsNotExist(err) {
		t.Errorf("a staged file was left behind at %s", target+stagedSuffix)
	}
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

// useInstallPath points the package at a temporary target for one test, so the
// swap and its rollback can be exercised without touching the real one.
func useInstallPath(t *testing.T, path string) {
	t.Helper()
	previous := installPath
	installPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { installPath = previous })
}

func TestSwapReplacesTheInstalledBinary(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, filepath.Join(dir, "bokkie"), "old")
	source := writeFile(t, filepath.Join(dir, "release", "bokkie"), "new")
	useInstallPath(t, target)

	if _, err := Swap(quietLogger(), source); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if got := readFile(t, target); got != "new" {
		t.Errorf("installed binary contains %q, want %q", got, "new")
	}
}

// The rollback is the whole reason an update is safe to run on a machine nobody
// is watching, so it gets a test of its own rather than being assumed.
func TestSwapRollsBackToTheBinaryItReplaced(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, filepath.Join(dir, "bokkie"), "old")
	source := writeFile(t, filepath.Join(dir, "release", "bokkie"), "new")
	useInstallPath(t, target)

	restore, err := Swap(quietLogger(), source)
	if err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if err := restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := readFile(t, target); got != "old" {
		t.Errorf("after rollback the installed binary contains %q, want %q", got, "old")
	}
	if _, err := os.Stat(target + displacedSuffix); !os.IsNotExist(err) {
		t.Errorf("the rejected binary was left at %s", target+displacedSuffix)
	}
	// The release folder is not ours to tidy up — it is somebody's USB stick.
	if got := readFile(t, source); got != "new" {
		t.Errorf("the release folder was disturbed: source contains %q", got)
	}
}

// Installing onto a host that has no bokkie yet still has to be reversible.
func TestSwapRollsBackToNothingWhenThereWasNothing(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bokkie")
	source := writeFile(t, filepath.Join(dir, "release", "bokkie"), "new")
	useInstallPath(t, target)

	restore, err := Swap(quietLogger(), source)
	if err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if err := restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("rollback left a binary at %s, but there was none before the swap", target)
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
