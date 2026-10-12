package auth

import (
	"sync"
	"time"
)

// Rate limiting for failed logins.
//
// The counter is in memory, not in the database, and that is a decision rather
// than a shortcut. A restart clearing the lockouts is a feature: it gives the
// owner a way to unlock a genuinely locked-out employee without a SQL shell
// ("restart the service"), and an attacker cannot restart the service. Against
// the actual threat here — someone on the station LAN guessing a short password
// — an in-memory counter is exactly as effective as a persisted one.
const (
	// maxFailures before the username is locked. Five is generous enough that
	// an honest typo-prone morning does not lock anyone out.
	maxFailures = 5

	// failureWindow is how long failures accumulate over. Failures older than
	// this are forgotten, so three wrong tries on Monday plus two on Friday is
	// not a lockout.
	failureWindow = 15 * time.Minute

	// lockoutDuration is how long a locked username stays locked.
	lockoutDuration = 15 * time.Minute
)

// Limiter tracks failed logins per username.
//
// Per username rather than per IP: the station is behind one router, so every
// counter PC may share an address, and locking by IP would let one person's
// fat-fingering lock out the whole station.
type Limiter struct {
	mu    sync.Mutex
	state map[string]*attempts
	now   func() time.Time // injectable so the tests need not sleep
}

type attempts struct {
	failures   []time.Time
	lockedTill time.Time
}

// NewLimiter returns an empty limiter.
func NewLimiter() *Limiter {
	return &Limiter{state: make(map[string]*attempts), now: time.Now}
}

// Locked reports whether username is currently locked out, and for how long.
// The remaining duration is told to the user: "try again in 12 minutes" is
// actionable, "too many attempts" leaves them retrying pointlessly.
func (l *Limiter) Locked(username string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	a, ok := l.state[username]
	if !ok {
		return false, 0
	}
	remaining := a.lockedTill.Sub(l.now())
	if remaining <= 0 {
		return false, 0
	}
	return true, remaining.Round(time.Second)
}

// Fail records a failed attempt and locks the username once it has failed too
// often within the window.
func (l *Limiter) Fail(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	a, ok := l.state[username]
	if !ok {
		a = &attempts{}
		l.state[username] = a
	}

	cutoff := now.Add(-failureWindow)
	kept := a.failures[:0]
	for _, at := range a.failures {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	a.failures = append(kept, now)

	if len(a.failures) >= maxFailures {
		a.lockedTill = now.Add(lockoutDuration)
		a.failures = nil
	}

	l.evictLocked(now)
}

// Succeed clears the history for a username after a successful login.
func (l *Limiter) Succeed(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.state, username)
}

// evictLocked drops entries that can no longer affect a decision, so a
// long-running process cannot accumulate a map entry per guessed username.
// Called under l.mu.
func (l *Limiter) evictLocked(now time.Time) {
	cutoff := now.Add(-failureWindow)
	for username, a := range l.state {
		if a.lockedTill.After(now) {
			continue
		}
		if len(a.failures) == 0 || a.failures[len(a.failures)-1].Before(cutoff) {
			delete(l.state, username)
		}
	}
}
