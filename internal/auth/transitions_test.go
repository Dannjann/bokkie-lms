package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// The transitions all take a transaction, so every test here opens one. inTx
// runs fn in a committed transaction and fails the test if anything goes wrong
// that the test did not expect.
func inTx(t *testing.T, db *sql.DB, fn func(tx *sql.Tx) error) error {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return nil
}

func seedUser(t *testing.T, db *sql.DB, username string, role Role) User {
	t.Helper()
	users := NewUsers(db)
	user, err := users.Create(context.Background(), username, "", "a-good-long-password", role)
	if err != nil {
		t.Fatalf("create %s: %v", username, err)
	}
	return user
}

func TestSetActiveTurnsAnAccountOffAndOnAgain(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rosa := seedUser(t, db, "rosa", RoleEncoder)

	var got User
	_ = inTx(t, db, func(tx *sql.Tx) error {
		var err error
		got, err = SetActiveTx(ctx, tx, rosa.ID, false)
		return err
	})
	if got.Active {
		t.Error("the account is still active after being deactivated")
	}

	// Authenticate is where deactivation has to bite, and it must bite with
	// its own error rather than looking like a wrong password — the audit
	// entry months later needs to say which it was.
	if _, err := NewUsers(db).Authenticate(ctx, "rosa", "a-good-long-password"); !errors.Is(err, ErrUserInactive) {
		t.Errorf("authenticate after deactivation = %v, want ErrUserInactive", err)
	}

	_ = inTx(t, db, func(tx *sql.Tx) error {
		var err error
		got, err = SetActiveTx(ctx, tx, rosa.ID, true)
		return err
	})
	if !got.Active {
		t.Error("the account is still inactive after being reactivated")
	}
	if _, err := NewUsers(db).Authenticate(ctx, "rosa", "a-good-long-password"); err != nil {
		t.Errorf("authenticate after reactivation: %v", err)
	}
}

func TestSetRoleRefusesARoleThatDoesNotExist(t *testing.T) {
	db := openTestDB(t)
	rosa := seedUser(t, db, "rosa", RoleEncoder)

	err := inTx(t, db, func(tx *sql.Tx) error {
		_, err := SetRoleTx(context.Background(), tx, rosa.ID, Role("supervisor"))
		return err
	})
	var ve ValidationError
	if !errors.As(err, &ve) || ve.Field != "role" {
		t.Fatalf("set role to supervisor = %v, want a ValidationError on role", err)
	}
}

func TestATransitionAgainstAnAccountThatIsNotThereIsNotFound(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// All three, because each one runs its own UPDATE and each has to tell
	// "no such account" apart from "changed it to what it already was" — the
	// distinction the API needs to answer 404 rather than 200.
	cases := map[string]func(tx *sql.Tx) error{
		"active": func(tx *sql.Tx) error { _, err := SetActiveTx(ctx, tx, 9999, false); return err },
		"role":   func(tx *sql.Tx) error { _, err := SetRoleTx(ctx, tx, 9999, RoleManager); return err },
		"password": func(tx *sql.Tx) error {
			_, err := SetPasswordTx(ctx, tx, 9999, "a-good-long-password")
			return err
		},
	}
	for name, fn := range cases {
		if err := inTx(t, db, fn); !errors.Is(err, ErrNoSuchUser) {
			t.Errorf("%s against a missing account = %v, want ErrNoSuchUser", name, err)
		}
	}
}

func TestSetPasswordEnforcesTheLengthFloorAndDoesNotWriteOnRefusal(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rosa := seedUser(t, db, "rosa", RoleEncoder)

	err := inTx(t, db, func(tx *sql.Tx) error {
		_, err := SetPasswordTx(ctx, tx, rosa.ID, "short")
		return err
	})
	var ve ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("set password to \"short\" = %v, want a ValidationError", err)
	}

	// The old password must still work: a refused change that had already
	// written the hash would lock the account out with no error to explain it.
	if _, err := NewUsers(db).Authenticate(ctx, "rosa", "a-good-long-password"); err != nil {
		t.Errorf("the old password stopped working after a refused change: %v", err)
	}
}

func TestVerifyPasswordTxSeparatesItsFailures(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rosa := seedUser(t, db, "rosa", RoleEncoder)

	if err := inTx(t, db, func(tx *sql.Tx) error {
		return VerifyPasswordTx(ctx, tx, rosa.ID, "a-good-long-password")
	}); err != nil {
		t.Errorf("the right password did not verify: %v", err)
	}

	if err := inTx(t, db, func(tx *sql.Tx) error {
		return VerifyPasswordTx(ctx, tx, rosa.ID, "not-the-password")
	}); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("a wrong password = %v, want ErrWrongPassword", err)
	}

	if err := inTx(t, db, func(tx *sql.Tx) error {
		return VerifyPasswordTx(ctx, tx, 9999, "a-good-long-password")
	}); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("a missing account = %v, want ErrNoSuchUser", err)
	}
}

func TestChangingAPasswordDoesNotChangeAnyoneElses(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rosa := seedUser(t, db, "rosa", RoleEncoder)
	seedUser(t, db, "nilo", RoleEncoder)

	_ = inTx(t, db, func(tx *sql.Tx) error {
		_, err := SetPasswordTx(ctx, tx, rosa.ID, "a-different-long-password")
		return err
	})

	users := NewUsers(db)
	if _, err := users.Authenticate(ctx, "rosa", "a-different-long-password"); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
	if _, err := users.Authenticate(ctx, "rosa", "a-good-long-password"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("the old password still works: %v", err)
	}
	if _, err := users.Authenticate(ctx, "nilo", "a-good-long-password"); err != nil {
		t.Errorf("another account's password was changed too: %v", err)
	}
}

func TestSessionsForOneUserAreEndedAndOthersAreNot(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rosa := seedUser(t, db, "rosa", RoleEncoder)
	nilo := seedUser(t, db, "nilo", RoleEncoder)

	sessions := NewSessions(db)
	keptToken, kept, err := sessions.Create(ctx, rosa.ID, "192.168.1.14")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	endedToken, _, err := sessions.Create(ctx, rosa.ID, "192.168.1.15")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	othersToken, _, err := sessions.Create(ctx, nilo.ID, "192.168.1.16")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	var n int64
	_ = inTx(t, db, func(tx *sql.Tx) error {
		var err error
		n, err = DeleteSessionsForUserTx(ctx, tx, rosa.ID, kept.ID)
		return err
	})
	if n != 1 {
		t.Errorf("deleted %d sessions, want 1", n)
	}

	if _, err := sessions.Lookup(ctx, keptToken); err != nil {
		t.Errorf("the spared session was ended: %v", err)
	}
	if _, err := sessions.Lookup(ctx, endedToken); !errors.Is(err, ErrNoSession) {
		t.Errorf("the other session survived: %v", err)
	}
	if _, err := sessions.Lookup(ctx, othersToken); err != nil {
		t.Errorf("another account's session was ended: %v", err)
	}

	// Sparing nothing ends every one of them, which is what a manager's reset
	// of somebody else's password has to do.
	_ = inTx(t, db, func(tx *sql.Tx) error {
		_, err := DeleteSessionsForUserTx(ctx, tx, rosa.ID, "")
		return err
	})
	if _, err := sessions.Lookup(ctx, keptToken); !errors.Is(err, ErrNoSession) {
		t.Errorf("a session survived a sweep that spared nothing: %v", err)
	}
}

func TestCountActiveManagersIgnoresEncodersAndDeactivatedManagers(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedUser(t, db, "dan", RoleManager)
	retired := seedUser(t, db, "lola", RoleManager)
	seedUser(t, db, "rosa", RoleEncoder)

	_ = inTx(t, db, func(tx *sql.Tx) error {
		_, err := SetActiveTx(ctx, tx, retired.ID, false)
		return err
	})

	var count int
	_ = inTx(t, db, func(tx *sql.Tx) error {
		var err error
		count, err = CountActiveManagersTx(ctx, tx)
		return err
	})
	if count != 1 {
		t.Errorf("active managers = %d, want 1 — an encoder or a deactivated manager was counted", count)
	}
}

// Twenty writers demoting the two managers at once. Whatever the outcome, the
// station must be left able to administer itself.
//
// This also measures the thing that decided the ordering: with the UPDATE
// first, roughly half the writers commit and the rest get a clean ErrLastManager.
// With the count first — see the barrier test below for why that is still safe —
// SQLite kills nineteen of twenty with a snapshot conflict, which the API would
// have to surface as a 500 rather than as a sentence anybody can read.
func TestConcurrentDemotionsCannotLeaveTheStationWithoutAManager(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	dan := seedUser(t, db, "dan", RoleManager)
	lola := seedUser(t, db, "lola", RoleManager)
	targets := []int64{dan.ID, lola.ID}

	const writers = 20
	var committed, refused atomic.Int64
	var wg sync.WaitGroup

	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return
			}
			defer tx.Rollback()

			// Write first, then count. This is the ordering under test.
			if _, err := SetRoleTx(ctx, tx, targets[i%len(targets)], RoleEncoder); err != nil {
				return
			}
			count, err := CountActiveManagersTx(ctx, tx)
			if err != nil {
				return
			}
			if count == 0 {
				refused.Add(1)
				return // rolled back by the defer
			}
			if tx.Commit() == nil {
				committed.Add(1)
			}
		}(i)
	}
	wg.Wait()

	var left int
	_ = inTx(t, db, func(tx *sql.Tx) error {
		var err error
		left, err = CountActiveManagersTx(ctx, tx)
		return err
	})
	if left < 1 {
		t.Fatalf("after %d concurrent demotions there are %d managers left — "+
			"the station cannot administer itself", writers, left)
	}
	if refused.Load() == 0 {
		t.Errorf("no writer was turned away by the guard; the survivor is an "+
			"accident rather than the guard working (committed=%d)", committed.Load())
	}
	t.Logf("committed=%d refused=%d managersLeft=%d", committed.Load(), refused.Load(), left)
}

func TestTheSameGuardHoldsForConcurrentDeactivations(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	dan := seedUser(t, db, "dan", RoleManager)
	lola := seedUser(t, db, "lola", RoleManager)
	targets := []int64{dan.ID, lola.ID}

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return
			}
			defer tx.Rollback()

			if _, err := SetActiveTx(ctx, tx, targets[i%len(targets)], false); err != nil {
				return
			}
			count, err := CountActiveManagersTx(ctx, tx)
			if err != nil || count == 0 {
				return
			}
			_ = tx.Commit()
		}(i)
	}
	wg.Wait()

	var left int
	_ = inTx(t, db, func(tx *sql.Tx) error {
		var err error
		left, err = CountActiveManagersTx(ctx, tx)
		return err
	})
	if left < 1 {
		t.Fatalf("after 20 concurrent deactivations there are %d managers left", left)
	}
}

// **This is the mutation, and it is not the one the plan predicted.**
//
// The plan said counting before the UPDATE would lose the last manager. It does
// not: inside a transaction, a count-then-write is a read-then-write, and WAL
// gives the second transaction a snapshot conflict its UPDATE cannot recover
// from. SQLite defends that shape for us.
//
// What actually loses the station its manager is taking the count *outside* the
// transaction — a guard read on one connection and the write on another. There
// is no snapshot to conflict with, so both writers pass a guard that was true
// when they read it and false by the time they acted on it.
//
// Two writers, each reading the count, both held at a barrier until both have
// read, and only then allowed to write. That is a check-then-act window held
// open on purpose: two managers clicking "Make encoder" in the same second.
func TestTheManagerGuardHasToBeInsideTheTransaction(t *testing.T) {
	// The wrong shape: guard outside. Both writers commit and nobody is left.
	if left := demoteBothAtABarrier(t, guardOutsideTransaction); left != 0 {
		t.Errorf("a guard read outside the transaction left %d managers; this "+
			"mutation is supposed to lose them all, so the test below proves "+
			"less than it claims", left)
	}

	// The shape we ship. One commits, one is refused, the station keeps a
	// manager — through the identical window.
	if left := demoteBothAtABarrier(t, guardInsideTransaction); left != 1 {
		t.Errorf("the in-transaction guard left %d managers, want 1", left)
	}
}

type guardShape int

const (
	guardOutsideTransaction guardShape = iota
	guardInsideTransaction
)

// demoteBothAtABarrier has two goroutines demote the two managers, each doing
// its guard read, then both waiting until both have read, then writing. It
// returns how many managers survive.
func demoteBothAtABarrier(t *testing.T, shape guardShape) int {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	dan := seedUser(t, db, "dan", RoleManager)
	lola := seedUser(t, db, "lola", RoleManager)
	targets := []int64{dan.ID, lola.ID}

	var read, release, done sync.WaitGroup
	read.Add(2)
	release.Add(1)
	done.Add(2)

	for i := range 2 {
		go func(i int) {
			defer done.Done()
			target := targets[i]

			if shape == guardOutsideTransaction {
				var n int
				_ = db.QueryRowContext(ctx,
					`SELECT count(*) FROM users WHERE active = 1 AND role = ?`,
					string(RoleManager)).Scan(&n)
				read.Done()
				release.Wait()
				if n <= 1 {
					return
				}
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					return
				}
				defer tx.Rollback()
				if _, err := SetRoleTx(ctx, tx, target, RoleEncoder); err != nil {
					return
				}
				_ = tx.Commit()
				return
			}

			read.Done()
			release.Wait()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return
			}
			defer tx.Rollback()
			if _, err := SetRoleTx(ctx, tx, target, RoleEncoder); err != nil {
				return
			}
			n, err := CountActiveManagersTx(ctx, tx)
			if err != nil || n == 0 {
				return
			}
			_ = tx.Commit()
		}(i)
	}

	read.Wait()
	release.Done()
	done.Wait()

	var left int
	_ = inTx(t, db, func(tx *sql.Tx) error {
		var err error
		left, err = CountActiveManagersTx(ctx, tx)
		return err
	})
	return left
}

// A hash must never reach a caller. User has no field for one, which is the
// structural half of the guarantee; this is the half that checks the JSON.
func TestATransitionNeverReturnsAHash(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rosa := seedUser(t, db, "rosa", RoleEncoder)

	var got User
	_ = inTx(t, db, func(tx *sql.Tx) error {
		var err error
		got, err = SetPasswordTx(ctx, tx, rosa.ID, "a-brand-new-password")
		return err
	})

	rendered := strings.ToLower(userAsText(t, got))
	for _, needle := range []string{"argon2", "hash", "a-brand-new-password"} {
		if strings.Contains(rendered, needle) {
			t.Errorf("a transition's result mentions %q: %s", needle, rendered)
		}
	}
}

func userAsText(t *testing.T, u User) string {
	t.Helper()
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal user: %v", err)
	}
	return string(b)
}
