package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bokkiedb "github.com/Dannjann/bokkie-lms/internal/db"
	"golang.org/x/crypto/argon2"
)

func TestPasswordHashingRoundTrips(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash = %q, want argon2id in PHC format", hash)
	}
	if strings.Contains(hash, "correct-horse") {
		t.Fatal("the password appears in its own hash")
	}

	ok, err := VerifyPassword(hash, "correct-horse-battery")
	if err != nil || !ok {
		t.Errorf("the right password did not verify: ok=%v err=%v", ok, err)
	}

	ok, err = VerifyPassword(hash, "correct-horse-batterz")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ok {
		t.Error("a wrong password verified")
	}
}

// The salt is what stops one cracked password from revealing every account that
// shares it — and staff at one counter will share passwords.
func TestTheSamePasswordHashesDifferentlyEveryTime(t *testing.T) {
	first, err := HashPassword("shared-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	second, err := HashPassword("shared-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if first == second {
		t.Error("two hashes of the same password are identical; the salt is not doing its job")
	}
}

// Cost parameters travel inside the hash, which is what allows them to be
// raised later without forcing everyone to reset their password. This proves
// it: a hash written with the cheap parameters of a slower machine still
// verifies against today's code, which uses higher ones.
func TestAHashVerifiesWithTheParametersItWasWrittenWith(t *testing.T) {
	const password = "correct-horse-battery"

	salt := []byte("sixteen-byte-slt")
	cheapMemory, cheapTime, cheapThreads := uint32(8*1024), uint32(1), uint8(1)
	key := argon2.IDKey([]byte(password), salt, cheapTime, cheapMemory, cheapThreads, argonKeyLen)

	cheap := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, cheapMemory, cheapTime, cheapThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))

	if cheapMemory == argonMemory && cheapTime == argonTime {
		t.Fatal("this test needs parameters that differ from the current ones")
	}

	ok, err := VerifyPassword(cheap, password)
	if err != nil {
		t.Fatalf("verify a cheaper hash: %v", err)
	}
	if !ok {
		t.Error("a hash written with lower cost parameters no longer verifies; " +
			"raising the cost would lock every existing user out")
	}
}

func TestMalformedHashesAreRejectedNotSilentlyFalse(t *testing.T) {
	for name, hash := range map[string]string{
		"empty":            "",
		"not argon":        "$2y$10$abcdefghijklmnopqrstuv",
		"missing sections": "$argon2id$v=19$m=65536,t=3,p=4$",
		"bad base64":       "$argon2id$v=19$m=65536,t=3,p=4$!!!$!!!",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyPassword(hash, "password"); err == nil {
				t.Error("a malformed hash verified without complaint")
			}
		})
	}
}

func TestUsernameAndPasswordRules(t *testing.T) {
	bad := map[string]string{
		"too short":       "ab",
		"starts with dot": ".dan",
		"has a space":     "dan p",
		"uppercase":       "Dan",
		"too long":        strings.Repeat("a", 33),
	}
	for name, username := range bad {
		t.Run("username "+name, func(t *testing.T) {
			var ve ValidationError
			if err := ValidateUsername(username); !errors.As(err, &ve) {
				t.Errorf("%q was accepted", username)
			}
		})
	}

	if err := ValidateUsername("dan.pallera-2"); err != nil {
		t.Errorf("a reasonable username was rejected: %v", err)
	}

	var ve ValidationError
	if err := ValidatePassword("short"); !errors.As(err, &ve) {
		t.Error("a five-character password was accepted")
	}
	if err := ValidatePassword("long-enough"); err != nil {
		t.Errorf("a reasonable password was rejected: %v", err)
	}
}

func TestFirstAdminCanOnlyBeCreatedOnce(t *testing.T) {
	ctx := context.Background()
	users := NewUsers(openTestDB(t))

	needs, err := users.NeedsSetup(ctx)
	if err != nil || !needs {
		t.Fatalf("NeedsSetup = %v, %v on an empty database; want true", needs, err)
	}

	admin, err := users.CreateFirstAdmin(ctx, "dan", "Dan P", "correct-horse")
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	if admin.Role != RoleManager {
		t.Errorf("first admin has role %q, want manager", admin.Role)
	}

	if needs, _ := users.NeedsSetup(ctx); needs {
		t.Error("NeedsSetup is still true after an admin exists")
	}
	if _, err := users.CreateFirstAdmin(ctx, "intruder", "", "another-password"); !errors.Is(err, ErrAlreadySetUp) {
		t.Errorf("second CreateFirstAdmin gave %v, want ErrAlreadySetUp", err)
	}
}

func TestAuthenticateDistinguishesItsFailures(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	users := NewUsers(database)

	if _, err := users.Create(ctx, "clerk", "Clerk", "encoder-password", RoleEncoder); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := users.Authenticate(ctx, "clerk", "encoder-password"); err != nil {
		t.Errorf("the right password was refused: %v", err)
	}
	if _, err := users.Authenticate(ctx, "clerk", "wrong"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong password gave %v, want ErrWrongPassword", err)
	}
	if _, err := users.Authenticate(ctx, "ghost", "anything"); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("unknown user gave %v, want ErrNoSuchUser", err)
	}

	// Usernames are matched case-insensitively: someone typing "Clerk" with the
	// caps lock on at 7am should get in.
	if _, err := users.Authenticate(ctx, "CLERK", "encoder-password"); err != nil {
		t.Errorf("an uppercase username was refused: %v", err)
	}

	if _, err := database.Exec(`UPDATE users SET active = 0 WHERE username = 'clerk'`); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if _, err := users.Authenticate(ctx, "clerk", "encoder-password"); !errors.Is(err, ErrUserInactive) {
		t.Errorf("deactivated account gave %v, want ErrUserInactive", err)
	}
}

func TestUsernamesAreUnique(t *testing.T) {
	ctx := context.Background()
	users := NewUsers(openTestDB(t))

	if _, err := users.Create(ctx, "clerk", "Clerk", "encoder-password", RoleEncoder); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := users.Create(ctx, "CLERK", "Other Clerk", "another-password", RoleEncoder); !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("a duplicate username (differing only in case) gave %v, want ErrUsernameTaken", err)
	}
}

// Sessions live in SQLite so they survive a restart. This is the closest a unit
// test gets to proving it: close the pool, reopen it, and look the token up
// through a store that has never seen it.
func TestASessionSurvivesTheProcessThatCreatedIt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "bokkie.db")

	database := openAt(t, path)
	users := NewUsers(database)
	user, err := users.Create(ctx, "clerk", "Clerk", "encoder-password", RoleEncoder)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	token, _, err := NewSessions(database).Create(ctx, user.ID, "192.168.1.20")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	database.Close()

	// A new process, in every way that matters here.
	reopened := openAt(t, path)
	sess, err := NewSessions(reopened).Lookup(ctx, token)
	if err != nil {
		t.Fatalf("the session did not survive the restart: %v", err)
	}
	if sess.UserID != user.ID {
		t.Errorf("session belongs to user %d, want %d", sess.UserID, user.ID)
	}
}

// The stored row must not be usable as a login by itself: backups of this
// database get carried around on a USB stick.
func TestTheSessionTokenIsNotStored(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	user, err := NewUsers(database).Create(ctx, "clerk", "Clerk", "encoder-password", RoleEncoder)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	sessions := NewSessions(database)
	token, sess, err := sessions.Create(ctx, user.ID, "192.168.1.20")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if sess.ID == token {
		t.Fatal("the session row id is the cookie token; a copy of the database would hand out live sessions")
	}

	var stored string
	if err := database.QueryRow(`SELECT id FROM sessions`).Scan(&stored); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if strings.Contains(stored, token) {
		t.Error("the token appears in the stored row")
	}

	// And the stored id must not work as a token.
	if _, err := sessions.Lookup(ctx, stored); !errors.Is(err, ErrNoSession) {
		t.Error("the stored id was accepted as a cookie value")
	}
}

func TestExpiredSessionsAreRefusedAndSweptUp(t *testing.T) {
	ctx := context.Background()
	database := openTestDB(t)
	user, err := NewUsers(database).Create(ctx, "clerk", "Clerk", "encoder-password", RoleEncoder)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	sessions := NewSessions(database)
	token, sess, err := sessions.Create(ctx, user.ID, "")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if _, err := database.Exec(`UPDATE sessions SET expires_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano), sess.ID); err != nil {
		t.Fatalf("expire session: %v", err)
	}

	if _, err := sessions.Lookup(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("an expired session was accepted: %v", err)
	}

	var count int
	if err := database.QueryRow(`SELECT count(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 0 {
		t.Errorf("%d expired sessions remain; Lookup should clear the row it just rejected", count)
	}
}

func TestRateLimiterLocksAfterRepeatedFailures(t *testing.T) {
	now := time.Now()
	limiter := NewLimiter()
	limiter.now = func() time.Time { return now }

	for i := 0; i < maxFailures-1; i++ {
		limiter.Fail("clerk")
	}
	if locked, _ := limiter.Locked("clerk"); locked {
		t.Fatalf("locked after only %d failures; the limit is %d", maxFailures-1, maxFailures)
	}

	limiter.Fail("clerk")
	locked, remaining := limiter.Locked("clerk")
	if !locked {
		t.Fatal("not locked after reaching the limit")
	}
	if remaining <= 0 || remaining > lockoutDuration {
		t.Errorf("remaining = %v, want between 0 and %v", remaining, lockoutDuration)
	}

	// Another username is unaffected: the station shares one router, so
	// locking anything broader would let one person's typos stop the counter.
	if locked, _ := limiter.Locked("boss"); locked {
		t.Error("locking one username locked another")
	}

	now = now.Add(lockoutDuration + time.Second)
	if locked, _ := limiter.Locked("clerk"); locked {
		t.Error("still locked after the lockout elapsed")
	}
}

func TestOldFailuresDoNotAccumulateIntoALockout(t *testing.T) {
	now := time.Now()
	limiter := NewLimiter()
	limiter.now = func() time.Time { return now }

	// Three wrong tries this morning...
	for i := 0; i < 3; i++ {
		limiter.Fail("clerk")
	}
	now = now.Add(failureWindow + time.Minute)
	// ...and two this afternoon must not add up to a lockout.
	limiter.Fail("clerk")
	limiter.Fail("clerk")

	if locked, _ := limiter.Locked("clerk"); locked {
		t.Error("failures from outside the window counted toward the limit")
	}
}

func TestASuccessfulLoginClearsTheCount(t *testing.T) {
	limiter := NewLimiter()
	for i := 0; i < maxFailures-1; i++ {
		limiter.Fail("clerk")
	}
	limiter.Succeed("clerk")
	limiter.Fail("clerk")

	if locked, _ := limiter.Locked("clerk"); locked {
		t.Error("a single failure after a success caused a lockout")
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return openAt(t, filepath.Join(t.TempDir(), "bokkie.db"))
}

func openAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	ctx := context.Background()

	database, err := bokkiedb.Open(ctx, path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := bokkiedb.Migrate(ctx, database, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}
