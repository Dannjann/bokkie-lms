// Command bokkie-release signs a release, and is a developer tool — it is never
// built into dist/ and never reaches the station.
//
// It exists because the private half of the release key must not be anywhere
// near the binary that verifies signatures with the public half. Keeping the
// two in separate programs makes that structural rather than a matter of
// remembering.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/release"
)

const usage = `bokkie-release — sign a bokkie release (developer tool)

usage: bokkie-release <command> [flags]

commands:
  keygen    create the release key pair, once, ever
  sign      write and sign latest.json for a folder of built binaries

The private key never leaves your machine and is never committed. Back it up
somewhere you will still have in five years: losing it means no installed host
can accept any future update.
`

// platforms maps the file names the Makefile writes to the GOOS/GOARCH the
// manifest records. A binary in the folder that is not named here is not part
// of a release.
var platforms = map[string]string{
	"bokkie-win-amd64.exe": "windows/amd64",
	"bokkie-mac-arm64":     "darwin/arm64",
	"bokkie-mac-amd64":     "darwin/amd64",
	"bokkie-linux-amd64":   "linux/amd64",
	"bokkie-linux-arm64":   "linux/arm64",
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "bokkie-release: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "bokkie-release:", err)
		os.Exit(1)
	}
}

// keygen writes the private key and prints the public one.
//
// It refuses to overwrite an existing key file. Generating a second key pair
// over the top of the first is unrecoverable in the way that matters: every
// host already running trusts the old public key, and no update signed with the
// new one would ever be accepted by them.
func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	path := fs.String("key", defaultKeyPath(), "where to write the private key")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if _, err := os.Stat(*path); err == nil {
		return fmt.Errorf("%s already exists.\n"+
			"Refusing to replace it: every host already installed trusts the public half of that key, "+
			"and nothing signed with a new one would be accepted by them. "+
			"Move it aside deliberately if you really mean to start over", *path)
	}

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate a key pair: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(*path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(*path), err)
	}
	encoded := base64.StdEncoding.EncodeToString(private) + "\n"
	if err := os.WriteFile(*path, []byte(encoded), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", *path, err)
	}

	fmt.Printf("private key written to %s (0600) — back this up off this machine\n\n", *path)
	fmt.Printf("paste this into internal/release/verify.go as the value of PublicKey:\n\n")
	fmt.Printf("var PublicKey = %q\n", base64.StdEncoding.EncodeToString(public))
	return nil
}

// sign writes latest.json describing every binary in dir, and latest.json.sig
// over it.
func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	keyPath := fs.String("key", defaultKeyPath(), "the private key to sign with")
	version := fs.String("version", "", "the release version, vX.Y.Z (default: the current git tag)")
	notes := fs.String("notes", "", "a line describing the release, shown before an update is applied")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := fs.Arg(0)
	if dir == "" {
		return fmt.Errorf("which folder? usage: bokkie-release sign [flags] <folder>")
	}

	if *version == "" {
		described, err := describeTag()
		if err != nil {
			return err
		}
		*version = described
	}
	if !release.IsRelease(*version) {
		return fmt.Errorf("%q is not a release version.\n"+
			"A release is an exact tag: git tag -a v0.3.0 -m '…' && make release", *version)
	}

	private, err := loadPrivateKey(*keyPath)
	if err != nil {
		return err
	}

	artifacts, err := describeArtifacts(dir)
	if err != nil {
		return err
	}

	manifest := release.Manifest{
		Version:   *version,
		Released:  time.Now().UTC().Format(time.RFC3339),
		Notes:     *notes,
		Artifacts: artifacts,
	}
	// Indented and newline-terminated because the exact bytes are what gets
	// signed, and a human reading the file has to be reading what was signed.
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the manifest: %w", err)
	}
	data = append(data, '\n')

	manifestPath := filepath.Join(dir, release.ManifestName)
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", manifestPath, err)
	}
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(private, data)) + "\n"
	signaturePath := filepath.Join(dir, release.SignatureName)
	if err := os.WriteFile(signaturePath, []byte(signature), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", signaturePath, err)
	}

	fmt.Printf("signed %s — %d binaries\n", manifest.Version, len(artifacts))
	for _, a := range artifacts {
		fmt.Printf("  %-14s %s\n", a.Platform, a.File)
	}
	return nil
}

// describeArtifacts measures every binary the Makefile produced. A missing one
// is a hard error: a release that quietly omits the Windows build is discovered
// at the worst possible moment, standing at the station.
func describeArtifacts(dir string) ([]release.Artifact, error) {
	artifacts := make([]release.Artifact, 0, len(platforms))
	for _, name := range sortedNames() {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("%s is missing from %s — run `make release` first: %w", name, dir, err)
		}
		sum, err := release.SHA256File(path)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, release.Artifact{
			Platform: platforms[name],
			File:     name,
			Size:     info.Size(),
			SHA256:   sum,
		})
	}
	return artifacts, nil
}

func sortedNames() []string {
	names := make([]string, 0, len(platforms))
	for name := range platforms {
		names = append(names, name)
	}
	// Map iteration order would otherwise reshuffle the manifest on every run,
	// making two identical releases produce different bytes.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the private key (run `bokkie-release keygen` if there is not one yet): %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		return nil, fmt.Errorf("%s is not a base64 private key: %w", path, err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s is %d bytes, want a %d-byte ed25519 private key", path, len(raw), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw), nil
}

func describeTag() (string, error) {
	output, err := exec.Command("git", "describe", "--tags", "--exact-match").Output()
	if err != nil {
		return "", fmt.Errorf("this commit is not tagged, so there is no version to sign.\n" +
			"Tag it first (git tag -a v0.3.0 -m '…'), or pass -version")
	}
	return strings.TrimSpace(string(output)), nil
}

func defaultKeyPath() string {
	if fromEnv := os.Getenv("BOKKIE_RELEASE_KEY"); fromEnv != "" {
		return fromEnv
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "bokkie-release-key"
	}
	return filepath.Join(home, ".config", "bokkie", "release-key")
}
