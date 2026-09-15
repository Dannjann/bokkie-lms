// Package release describes what a bokkie release is: a version, a manifest
// naming one binary per platform, and an ed25519 signature over that manifest.
//
// It is deliberately ignorant of *how* a release arrived. Today `bokkie update`
// is handed a folder from a USB stick; if that ever becomes an HTTPS fetch, the
// manifest, the signature, and every check in this package stay exactly as they
// are and only the thing that produces the bytes changes.
package release

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a release version: vMAJOR.MINOR.PATCH and nothing else.
//
// The strictness is the point. `git describe` produces four shapes — an exact
// tag (v0.3.0), a build after one (v0.3.0-2-gabc1234), a bare commit on a
// repository with no tags at all (abc1234), and `dev` from a plain `go build`.
// Only the first is a release. Treating the others as installable is how a
// half-finished build ends up on the station's host.
type Version struct {
	Major, Minor, Patch int
}

// ParseVersion accepts an exact release tag and rejects everything else,
// including the "-dirty" and "-N-gSHA" suffixes git adds to describe a build
// that is merely *near* a tag.
func ParseVersion(s string) (Version, error) {
	rest, ok := strings.CutPrefix(s, "v")
	if !ok {
		return Version{}, fmt.Errorf("%q is not a release version: it must look like v1.2.3", s)
	}

	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("%q is not a release version: it must look like v1.2.3", s)
	}

	var v Version
	for i, target := range []*int{&v.Major, &v.Minor, &v.Patch} {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("%q is not a release version: %q is not a number", s, parts[i])
		}
		// A leading zero means two spellings of one version, and two spellings
		// means a comparison that disagrees with a human reading the tag.
		if len(parts[i]) > 1 && parts[i][0] == '0' {
			return Version{}, fmt.Errorf("%q is not a release version: %q has a leading zero", s, parts[i])
		}
		*target = n
	}
	return v, nil
}

// IsRelease reports whether s is an exact release tag. A build that is not one
// can still be installed, but only when someone says so explicitly — see
// cmd/bokkie/update.go.
func IsRelease(s string) bool {
	_, err := ParseVersion(s)
	return err == nil
}

func (v Version) String() string {
	return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Compare returns -1, 0 or 1 as v sorts before, with, or after other.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]int{
		{v.Major, other.Major},
		{v.Minor, other.Minor},
		{v.Patch, other.Patch},
	} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// NewerThan is the question the update command actually asks. Installing the
// same version again is pointless; installing an older one is a downgrade that
// db.Migrate would refuse at the next boot anyway, having already replaced the
// binary.
func (v Version) NewerThan(other Version) bool { return v.Compare(other) > 0 }
