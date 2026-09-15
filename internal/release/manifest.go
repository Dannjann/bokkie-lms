package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// ManifestName and SignatureName are the two files a release folder must
// contain beside its binaries. They are fixed rather than configurable so that
// "point at the release folder" is the whole of what anyone has to know.
const (
	ManifestName  = "latest.json"
	SignatureName = "latest.json.sig"
)

// Manifest is the index of one release.
//
// Only the manifest is signed. It carries a SHA-256 for every binary, so one
// signature covers the whole release: tampering with a binary fails its hash,
// and tampering with the hash fails the signature.
type Manifest struct {
	Version   string     `json:"version"`
	Released  string     `json:"released"`
	Notes     string     `json:"notes,omitempty"`
	Artifacts []Artifact `json:"artifacts"`
}

// Artifact is one platform's binary within a release.
type Artifact struct {
	// Platform is GOOS/GOARCH, matched against the host at install time so a
	// linux/arm64 binary can never be copied onto a Windows host.
	Platform string `json:"platform"`
	File     string `json:"file"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

// ParseManifest reads a manifest and checks it is internally coherent, so that
// later code can trust its shape without re-checking every field.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	// An unknown field means this binary is reading a manifest written by a
	// newer bokkie than itself, and guessing at what it means is exactly the
	// wrong instinct where the answer decides which code runs as a service.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("read %s: %w", ManifestName, err)
	}

	if _, err := ParseVersion(m.Version); err != nil {
		return Manifest{}, fmt.Errorf("%s names %w", ManifestName, err)
	}
	if len(m.Artifacts) == 0 {
		return Manifest{}, fmt.Errorf("%s lists no binaries", ManifestName)
	}
	for _, a := range m.Artifacts {
		switch {
		case a.Platform == "":
			return Manifest{}, fmt.Errorf("%s has an artifact with no platform", ManifestName)
		case a.File == "":
			return Manifest{}, fmt.Errorf("%s has no file name for %s", ManifestName, a.Platform)
		case len(a.SHA256) != sha256.Size*2:
			return Manifest{}, fmt.Errorf("%s has a malformed sha256 for %s", ManifestName, a.Platform)
		case a.Size <= 0:
			return Manifest{}, fmt.Errorf("%s has no size for %s", ManifestName, a.Platform)
		}
	}
	return m, nil
}

// Platform is the host this binary was built for, in the manifest's spelling.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// ForHost returns the artifact this machine can actually run.
//
// A release that has no binary for the host is a normal thing to meet — it is
// what a station on an architecture we stopped building for would see — so it
// is an ordinary error rather than something to work around.
func (m Manifest) ForHost() (Artifact, error) {
	for _, a := range m.Artifacts {
		if a.Platform == Platform() {
			return a, nil
		}
	}
	return Artifact{}, fmt.Errorf("release %s has no binary for %s", m.Version, Platform())
}

// Verify checks a binary against the entry the signed manifest holds for it.
// Size is checked first only because it turns the common case — the wrong file
// entirely — into a clearer message than a hash mismatch would give.
func (a Artifact) Verify(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("find %s: %w", path, err)
	}
	if info.Size() != a.Size {
		return fmt.Errorf("%s is %d bytes, but the release says %d: this is not the file the manifest describes",
			filepath.Base(path), info.Size(), a.Size)
	}

	sum, err := SHA256File(path)
	if err != nil {
		return err
	}
	if sum != a.SHA256 {
		return fmt.Errorf("%s does not match the checksum in the signed release: it has been altered or corrupted since it was built",
			filepath.Base(path))
	}
	return nil
}

// SHA256File is exported for the signing tool, which has to produce exactly
// the value Artifact.Verify will later check.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
