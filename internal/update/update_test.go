package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dannjann/bokkie-lms/internal/backup"
	"github.com/Dannjann/bokkie-lms/internal/buildinfo"
	"github.com/Dannjann/bokkie-lms/internal/release"
)

// Every test here checks a refusal, and checks it left no backup behind.
//
// That second assertion is the real subject: a refusal has to happen before
// anything on the host changes. Once a backup has been written the sequence has
// committed to something, and these are all cases where it never should have
// started.

func TestApplyRefusesAReleaseThatIsNotSigned(t *testing.T) {
	dir, backupFolder := releaseFolder(t, "v2.0.0", running("v1.0.0"))
	if err := os.Remove(filepath.Join(dir, release.SignatureName)); err != nil {
		t.Fatalf("remove signature: %v", err)
	}

	err := apply(t, dir, backupFolder, false)
	if err == nil {
		t.Fatal("Apply accepted a release with no signature")
	}
	assertNoBackup(t, backupFolder)
}

func TestApplyRefusesAReleaseSignedByAnotherKey(t *testing.T) {
	dir, backupFolder := releaseFolder(t, "v2.0.0", running("v1.0.0"))

	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	previous := release.PublicKey
	release.PublicKey = base64.StdEncoding.EncodeToString(other)
	t.Cleanup(func() { release.PublicKey = previous })

	if err := apply(t, dir, backupFolder, false); err == nil {
		t.Fatal("Apply accepted a release signed by a key this build does not trust")
	}
	assertNoBackup(t, backupFolder)
}

func TestApplyRefusesABinaryAlteredAfterSigning(t *testing.T) {
	dir, backupFolder := releaseFolder(t, "v2.0.0", running("v1.0.0"))

	path := filepath.Join(dir, binaryName)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	content[0] ^= 0xff
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	if err := apply(t, dir, backupFolder, false); err == nil {
		t.Fatal("Apply accepted a binary that does not match the signed checksum")
	}
	assertNoBackup(t, backupFolder)
}

// Going backwards is how a host ends up running a binary that refuses to start
// against a schema the newer one already migrated.
func TestApplyRefusesAnOlderVersion(t *testing.T) {
	dir, backupFolder := releaseFolder(t, "v1.0.0", running("v2.0.0"))

	err := apply(t, dir, backupFolder, false)
	if err == nil {
		t.Fatal("Apply accepted a version older than the one running")
	}
	if !strings.Contains(err.Error(), "-force") {
		t.Errorf("error %q does not say how to do it deliberately", err)
	}
	assertNoBackup(t, backupFolder)
}

func TestApplyRefusesTheVersionAlreadyRunning(t *testing.T) {
	dir, backupFolder := releaseFolder(t, "v2.0.0", running("v2.0.0"))

	if err := apply(t, dir, backupFolder, false); err == nil {
		t.Fatal("Apply accepted the version already running")
	}
	assertNoBackup(t, backupFolder)
}

// A development build has no version to compare against, so replacing it has to
// be a deliberate act rather than an accident of a version string that sorts
// oddly.
func TestApplyRefusesToReplaceADevelopmentBuildByAccident(t *testing.T) {
	dir, backupFolder := releaseFolder(t, "v2.0.0", running("dev"))

	err := apply(t, dir, backupFolder, false)
	if err == nil {
		t.Fatal("Apply replaced a development build without being told to")
	}
	if !strings.Contains(err.Error(), "-force") {
		t.Errorf("error %q does not say how to do it deliberately", err)
	}
	assertNoBackup(t, backupFolder)
}

func TestApplyRefusesAReleaseWithNothingForThisHost(t *testing.T) {
	dir, backupFolder := releaseFolderForPlatform(t, "v2.0.0", running("v1.0.0"), "plan9/mips")

	if err := apply(t, dir, backupFolder, false); err == nil {
		t.Fatal("Apply accepted a release with no binary for this platform")
	}
	assertNoBackup(t, backupFolder)
}

const binaryName = "bokkie-test-binary"

// apply runs Apply with no database and no service, which is safe precisely
// because every case here refuses before it would need either.
func apply(t *testing.T, dir, backupFolder string, force bool) error {
	t.Helper()
	_, err := Apply(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		Dir:    dir,
		Backup: backup.Options{Folder: backupFolder, Retention: 2},
		Force:  force,
	})
	return err
}

func assertNoBackup(t *testing.T, folder string) {
	t.Helper()
	entries, err := os.ReadDir(folder)
	if err != nil {
		if os.IsNotExist(err) {
			return // never created: better still
		}
		t.Fatalf("read backup folder: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the update refused but wrote %d file(s) into the backup folder; nothing should have happened yet", len(entries))
	}
}

// running points buildinfo at a version for the duration of one test.
func running(version string) func(*testing.T) {
	return func(t *testing.T) {
		previous := buildinfo.Version
		buildinfo.Version = version
		t.Cleanup(func() { buildinfo.Version = previous })
	}
}

func releaseFolder(t *testing.T, version string, setRunning func(*testing.T)) (dir, backupFolder string) {
	return releaseFolderForPlatform(t, version, setRunning, release.Platform())
}

func releaseFolderForPlatform(t *testing.T, version string, setRunning func(*testing.T), platform string) (dir, backupFolder string) {
	t.Helper()
	setRunning(t)

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	previous := release.PublicKey
	release.PublicKey = base64.StdEncoding.EncodeToString(public)
	t.Cleanup(func() { release.PublicKey = previous })

	dir = t.TempDir()
	backupFolder = filepath.Join(t.TempDir(), "backups")

	path := filepath.Join(dir, binaryName)
	if err := os.WriteFile(path, []byte("a binary, for the purposes of this test"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat binary: %v", err)
	}
	sum, err := release.SHA256File(path)
	if err != nil {
		t.Fatalf("hash binary: %v", err)
	}

	data, err := json.MarshalIndent(release.Manifest{
		Version:   version,
		Released:  "2026-01-01T00:00:00Z",
		Artifacts: []release.Artifact{{Platform: platform, File: binaryName, Size: info.Size(), SHA256: sum}},
	}, "", "  ")
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(filepath.Join(dir, release.ManifestName), data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(private, data)) + "\n"
	if err := os.WriteFile(filepath.Join(dir, release.SignatureName), []byte(signature), 0o644); err != nil {
		t.Fatalf("write signature: %v", err)
	}
	return dir, backupFolder
}
