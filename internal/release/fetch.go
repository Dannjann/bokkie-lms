package release

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// LatestBase is where a published release's files are, always.
//
// GitHub redirects this to the newest release that is neither a draft nor a
// prerelease, so nothing here needs an API call, a token, or a rate limit to
// reason about — and a draft being checked by CI is invisible to it, which is
// what makes the draft-then-publish sequence safe.
const LatestBase = "https://github.com/Dannjann/bokkie-lms/releases/latest/download/"

// FetchTimeout is deliberately short. This runs with somebody standing at the
// machine watching a window, and a station behind a captive portal has to fall
// into the offline path rather than appear to have hung.
const FetchTimeout = 20 * time.Second

// maxManifestSize caps what will be read from the other end. A manifest is
// under a kilobyte; the other end is not ours to trust about length.
const maxManifestSize = 64 << 10

// Fetch retrieves a manifest and its signature over HTTPS and returns it only
// if the signature is genuine.
//
// Every check it applies is the one Load applies to a folder — this adds the
// download and nothing else, which is the contract step 2.5 wrote down.
func Fetch(ctx context.Context, client *http.Client, base string) (Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()

	data, err := get(ctx, client, base+ManifestName)
	if err != nil {
		return Manifest{}, err
	}
	encoded, err := get(ctx, client, base+SignatureName)
	if err != nil {
		return Manifest{}, err
	}
	signature, err := DecodeSignature(encoded)
	if err != nil {
		return Manifest{}, err
	}
	return LoadBytes(data, signature)
}

func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxManifestSize))
}

// DownloadManifest writes a release's manifest and signature into dir, so that
// the rest of the system can treat a download exactly as it treats a folder off
// a USB stick.
//
// It deliberately does not verify: Load does that, from the files this leaves
// behind, with the same code and the same messages the USB path has always
// used. Verifying here would be a second implementation of the one check that
// must never have two.
func DownloadManifest(ctx context.Context, client *http.Client, base, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()

	for _, name := range []string{ManifestName, SignatureName} {
		body, err := get(ctx, client, base+name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}
