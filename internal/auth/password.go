// Package auth owns identity: password hashing, users, sessions, and the
// rate limit that stands between a station PC on a shared LAN and a four-digit
// PIN culture.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters.
//
// These are the OWASP second-recommended profile (64 MiB, t=3) rather than the
// minimum. The station PC is modest, but a login happens a handful of times a
// day and nothing else in this system competes for memory in that instant, so
// paying ~50ms and 64 MiB per login is free in practice and buys a real margin
// against an offline crack of a stolen bokkie.db — which is a realistic threat
// here, because this same step starts writing backup copies onto a USB stick.
//
// The parameters are stored in every hash, so raising them later does not
// invalidate existing passwords: an old hash still verifies with the values it
// was written with.
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // KiB
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// argonThreads is capped so a hash written on a four-core station PC still
// verifies at the same cost on a single-core box — parallelism is recorded in
// the hash, and argon2 requires the same value to reproduce it.
var argonThreads = func() uint8 {
	if n := runtime.NumCPU(); n < 4 {
		return uint8(max(n, 1))
	}
	return 4
}()

// ErrHashInvalid means the stored hash is not something this code can verify —
// a truncated row, or a hash from a different algorithm.
var ErrHashInvalid = errors.New("password hash is malformed")

// HashPassword returns a PHC-format argon2id hash.
//
// The full format is stored, not just the digest: the salt and the cost
// parameters travel with the hash, which is what makes it possible to raise the
// cost later without a forced password reset for everyone.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password produced encoded.
//
// The comparison is constant-time. That matters less over a LAN than the
// textbook says, but it costs nothing and the alternative is explaining why it
// was skipped.
func VerifyPassword(encoded, password string) (bool, error) {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}

	got := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func decodeHash(encoded string) (params argonParams, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return params, nil, nil, ErrHashInvalid
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return params, nil, nil, ErrHashInvalid
	}
	if version != argon2.Version {
		return params, nil, nil, fmt.Errorf("%w: argon2 version %d is not supported", ErrHashInvalid, version)
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.memory, &params.time, &params.threads); err != nil {
		return params, nil, nil, ErrHashInvalid
	}

	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return params, nil, nil, ErrHashInvalid
	}
	if key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return params, nil, nil, ErrHashInvalid
	}
	if len(salt) == 0 || len(key) == 0 {
		return params, nil, nil, ErrHashInvalid
	}
	return params, salt, key, nil
}
