package auth

import (
	"fmt"
	"regexp"
)

// ValidationError is a complaint about what someone typed, as opposed to a
// fault in the system. The API turns it into a 400 with the message shown to
// the person at the counter, so the messages here are written for them and not
// for a log file.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string { return e.Message }

// usernamePattern keeps usernames to something that can be typed quickly on a
// counter keyboard and written unambiguously on a handover note.
var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

// minPasswordLength is a floor, not a policy. Complexity rules (one capital,
// one symbol) reliably produce "Password1!" on a sticky note; length alone is
// the requirement that actually helps here, and the rate limiter covers the
// guessing this leaves open.
const minPasswordLength = 8

// ValidateUsername and ValidatePassword are exported so the API can return the
// specific complaint rather than a generic "invalid".
func ValidateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return ValidationError{
			Field: "username",
			Message: "username must be 3 to 32 characters — lowercase letters, numbers, dot, dash " +
				"or underscore — and start with a letter or number",
		}
	}
	return nil
}

func ValidatePassword(password string) error {
	if len([]rune(password)) < minPasswordLength {
		return ValidationError{
			Field:   "password",
			Message: fmt.Sprintf("password must be at least %d characters", minPasswordLength),
		}
	}
	return nil
}
