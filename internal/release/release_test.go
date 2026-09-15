package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVersionAcceptsOnlyAnExactReleaseTag(t *testing.T) {
	good := []string{"v0.0.0", "v1.2.3", "v10.20.30"}
	for _, s := range good {
		if _, err := ParseVersion(s); err != nil {
			t.Errorf("ParseVersion(%q) = %v, want it accepted", s, err)
		}
	}

	// Every one of these is something `git describe` really produces, and none
	// of them is a release.
	bad := []string{
		"dev",               // a plain go build
		"abc1234",           // a repository with no tags
		"abc1234-dirty",     // …with uncommitted changes
		"v1.2.3-dirty",      // a tagged commit with uncommitted changes
		"v1.2.3-2-gabc1234", // two commits past the tag
		"1.2.3",             // no v
		"v1.2",              // not three parts
		"v1.2.3.4",          // four
		"v01.2.3",           // two spellings of one version
		"v1.2.x",            // not a number
		"",
	}
	for _, s := range bad {
		if _, err := ParseVersion(s); err == nil {
			t.Errorf("ParseVersion(%q) succeeded, want it refused", s)
		}
	}
}

func TestVersionOrdering(t *testing.T) {
	cases := []struct {
		a, b  string
		newer bool
	}{
		{"v1.0.0", "v0.9.9", true},
		{"v0.10.0", "v0.9.0", true}, // the comparison a string sort gets wrong
		{"v1.0.1", "v1.0.0", true},
		{"v1.0.0", "v1.0.0", false},
		{"v1.0.0", "v1.0.1", false},
	}
	for _, c := range cases {
		a, err := ParseVersion(c.a)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c.a, err)
		}
		b, err := ParseVersion(c.b)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c.b, err)
		}
		if got := a.NewerThan(b); got != c.newer {
			t.Errorf("%s newer than %s = %v, want %v", c.a, c.b, got, c.newer)
		}
	}
}

func TestLoadAcceptsAGenuineRelease(t *testing.T) {
	dir, _ := signedRelease(t, "v1.2.3")

	manifest, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if manifest.Version != "v1.2.3" {
		t.Errorf("version = %q, want v1.2.3", manifest.Version)
	}
}

// The signature is the only thing standing between a USB stick and code running
// as the account that owns the ledger.
func TestLoadRefusesAnAlteredManifest(t *testing.T) {
	dir, _ := signedRelease(t, "v1.2.3")

	path := filepath.Join(dir, ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	altered := strings.Replace(string(data), "v1.2.3", "v9.9.9", 1)
	if err := os.WriteFile(path, []byte(altered), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	if _, err := Load(dir); err == nil {
		t.Fatal("Load accepted a manifest that was edited after signing")
	}
}

func TestLoadRefusesAnotherKeysSignature(t *testing.T) {
	dir, _ := signedRelease(t, "v1.2.3")

	// Same manifest, signed by someone else entirely.
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	usePublicKey(t, other)

	if _, err := Load(dir); err == nil {
		t.Fatal("Load accepted a release signed by a different key")
	}
}

// A build with no key compiled in must refuse rather than accept anything.
func TestLoadRefusesWhenNoKeyIsCompiledIn(t *testing.T) {
	dir, _ := signedRelease(t, "v1.2.3")

	previous := PublicKey
	PublicKey = ""
	t.Cleanup(func() { PublicKey = previous })

	if _, err := Load(dir); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Load error = %v, want ErrNoKey", err)
	}
}

func TestParseManifestRefusesAFieldItDoesNotUnderstand(t *testing.T) {
	data := []byte(`{"version":"v1.0.0","released":"2026-01-01T00:00:00Z",
		"artifacts":[{"platform":"darwin/arm64","file":"b","size":1,"sha256":"` +
		strings.Repeat("a", 64) + `"}],"rollout":"staged"}`)

	if _, err := ParseManifest(data); err == nil {
		t.Fatal("ParseManifest accepted a field written by a newer bokkie than this one")
	}
}

func TestArtifactVerifyCatchesAnAlteredBinary(t *testing.T) {
	dir, artifact := signedRelease(t, "v1.2.3")
	path := filepath.Join(dir, artifact.File)

	if err := artifact.Verify(path); err != nil {
		t.Fatalf("Verify on the real file: %v", err)
	}

	// Same length, different bytes: the size check cannot catch this one, and
	// the hash has to.
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	altered := append([]byte{}, original...)
	altered[0] ^= 0xff
	if err := os.WriteFile(path, altered, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	if err := artifact.Verify(path); err == nil {
		t.Fatal("Verify accepted a binary whose contents changed after signing")
	}
}

// signedRelease builds a release folder holding one binary for this host,
// signed by a key generated for the test, and points PublicKey at it.
func signedRelease(t *testing.T, version string) (string, Artifact) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	usePublicKey(t, public)

	dir := t.TempDir()
	name := "bokkie-test-binary"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("a binary, for the purposes of this test"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat binary: %v", err)
	}
	sum, err := SHA256File(path)
	if err != nil {
		t.Fatalf("hash binary: %v", err)
	}

	artifact := Artifact{Platform: Platform(), File: name, Size: info.Size(), SHA256: sum}
	data, err := json.MarshalIndent(Manifest{
		Version:   version,
		Released:  "2026-01-01T00:00:00Z",
		Artifacts: []Artifact{artifact},
	}, "", "  ")
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(filepath.Join(dir, ManifestName), data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(private, data)) + "\n"
	if err := os.WriteFile(filepath.Join(dir, SignatureName), []byte(signature), 0o644); err != nil {
		t.Fatalf("write signature: %v", err)
	}
	return dir, artifact
}

func usePublicKey(t *testing.T, key ed25519.PublicKey) {
	t.Helper()
	previous := PublicKey
	PublicKey = base64.StdEncoding.EncodeToString(key)
	t.Cleanup(func() { PublicKey = previous })
}
