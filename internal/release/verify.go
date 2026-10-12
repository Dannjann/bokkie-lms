package release

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PublicKey is the ed25519 key every bokkie release is signed with, base64.
//
// It is committed source rather than a file beside the binary or a value passed
// at build time, and that is the whole design: a binary carries the key it will
// use to judge its own replacement. The release folder — a USB stick that spent
// a week in a drawer, or one day an HTTPS download — can then be completely
// untrusted, because nothing in it can alter the key deciding whether to
// believe it.
//
// A checksum alone would not do this job. It travels in the same folder, on the
// same stick, down the same wire as the binary, so whatever can alter one can
// alter both.
//
// Empty means no key was compiled in, and every update is refused. Failing shut
// is correct here: the alternative is a build that installs anything handed to
// it while looking exactly like one that checks.
var PublicKey = ""

// ErrNoKey is returned by a build with no public key compiled in.
var ErrNoKey = fmt.Errorf("this build has no release key compiled into it, so it cannot tell a genuine bokkie release from any other file; " +
	"install the update by hand or use a build made from a tagged release")

// Load reads the manifest in dir and returns it only if its signature is
// genuine. Nothing else in this package should be trusted before this has
// succeeded — the version, the file names and the checksums are all only as
// believable as the signature over them.
func Load(dir string) (Manifest, error) {
	manifestPath := filepath.Join(dir, ManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("read the release manifest: %w", err)
	}
	signature, err := readSignature(filepath.Join(dir, SignatureName))
	if err != nil {
		return Manifest{}, err
	}

	manifest, err := LoadBytes(data, signature)
	if err != nil {
		// Naming the file is most of the value of the message when the folder
		// came off a USB stick and there may be more than one of them.
		return Manifest{}, fmt.Errorf("%s: %w", manifestPath, err)
	}
	return manifest, nil
}

// LoadBytes verifies a manifest and its signature as bytes, wherever they came
// from — a folder on a USB stick, or an HTTPS response.
//
// This is the seam this package's comment promised: "if that ever becomes an
// HTTPS fetch, the manifest, the signature, and every check in this package
// stay exactly as they are and only the thing that produces the bytes changes."
func LoadBytes(data, signature []byte) (Manifest, error) {
	key, err := publicKey()
	if err != nil {
		return Manifest{}, err
	}
	if !ed25519.Verify(key, data, signature) {
		return Manifest{}, fmt.Errorf(
			"not signed by the bokkie release key: it was not produced by this project, or it has been altered since it was")
	}
	return ParseManifest(data)
}

// DecodeSignature reads the base64 a .sig file holds. Exported for the fetch,
// which has the bytes but no file to read them from.
func DecodeSignature(encoded []byte) ([]byte, error) { return decodeSignature(encoded, SignatureName) }

func publicKey() (ed25519.PublicKey, error) {
	if PublicKey == "" {
		return nil, ErrNoKey
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(PublicKey))
	if err != nil {
		return nil, fmt.Errorf("the release key compiled into this build is not valid base64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("the release key compiled into this build is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

func readSignature(path string) ([]byte, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the release signature: %w", err)
	}
	return decodeSignature(encoded, filepath.Base(path))
}

func decodeSignature(encoded []byte, name string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		return nil, fmt.Errorf("%s is not valid base64: %w", name, err)
	}
	if len(raw) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%s is %d bytes, want a %d-byte signature", name, len(raw), ed25519.SignatureSize)
	}
	return raw, nil
}
