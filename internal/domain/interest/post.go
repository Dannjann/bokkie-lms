package interest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
)

// PostTx charges a run: it allocates the run number and writes one ledger entry
// per line, all inside the caller's transaction.
//
// **The UPDATE below must be the first statement this transaction runs.**
//
// internal/db opens transactions deferred, so a transaction that reads first
// and writes second has to upgrade its lock — and SQLite refuses an upgrade
// that would deadlock immediately, without honouring busy_timeout. That is what
// made step 4's read-then-write allocator fail as SQLITE_BUSY on 43 of 50
// concurrent writers, and what step 5's and step 6's posting were shaped to
// avoid.
//
// This run has more to do after that first write than either of them, and every
// bit of it is therefore a check that looks like a precondition and is not:
//
//  1. UPDATE — the number, the status, who posted, and **only if no posted run
//     already covers this period**. That last condition is in the statement
//     rather than in a check before it, so that the period guard is applied by
//     the same write that takes the lock. A check beforehand would be a read,
//     and the unique index on (period_from, period_to) would in any case have
//     fired first — as a constraint error nobody can read, rather than as a
//     sentence naming the run that already covers the period.
//  2. Does the run exist, was it a draft, did the period guard refuse it.
//  3. Does it charge anything at all.
//  4. Recompute every line from the ledger *inside this transaction*, and
//     refuse if any of it has moved since the preview.
//  5. Write the entries, all at once.
//
// Each of 2 to 4 returns an error, the caller rolls back, and nothing happened.
func (s *Service) PostTx(ctx context.Context, tx *sql.Tx, id int64, by Actor) (Run, error) {
	if by.Username == "" {
		return Run{}, fmt.Errorf("an interest run must say who posted it")
	}

	// Allocated inside the UPDATE, never as a read followed by a write, with
	// the partial unique index on run_no as the backstop. MAX ignores the NULLs
	// the drafts carry, so the sequence counts posted runs only and has no gaps
	// in it.
	stamp := s.now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`UPDATE interest_runs
		    SET run_no = (SELECT COALESCE(MAX(run_no), 0) + 1 FROM interest_runs),
		        status = 'posted', posted_at = ?, posted_by = ?, posted_by_username = ?
		  WHERE id = ? AND status = 'draft'
		    AND NOT EXISTS (SELECT 1
		                      FROM interest_runs charged
		                     WHERE charged.status = 'posted'
		                       AND charged.period_from <= interest_runs.period_to
		                       AND charged.period_to   >= interest_runs.period_from)`,
		stamp, nullableID(by.UserID), by.Username, id)
	if err != nil {
		return Run{}, fmt.Errorf("post interest run %d: %w", id, err)
	}

	// Zero rows means one of three things, and they are three different
	// sentences: the run is not there, it was not a draft — which is what a
	// double-clicked Post button looks like, and why it charges once — or the
	// period has already been charged.
	affected, err := res.RowsAffected()
	if err != nil {
		return Run{}, fmt.Errorf("post interest run %d: %w", id, err)
	}

	run, err := byID(ctx, tx, id)
	if err != nil {
		return Run{}, err
	}
	if affected == 0 {
		if run.Status != StatusDraft {
			return Run{}, fmt.Errorf("%w: run %d is %s", ErrNotDraft, id, run.Status)
		}
		// Still a draft, so it was the period guard in the WHERE clause. The
		// query below is only there to name the run that covers it.
		if err := overlapping(ctx, tx, run); err != nil {
			return Run{}, err
		}
		return Run{}, fmt.Errorf("%w: %d", ErrPeriodAlreadyCharged, id)
	}

	if len(run.Lines) == 0 {
		return Run{}, fmt.Errorf("%w: %d", ErrNothingToCharge, id)
	}

	// The preview must still be true. This is the step that makes the whole
	// arrangement honest: a manager approved a set of figures, and these are
	// the figures being written.
	fresh, err := s.compute(ctx, tx, run.Rule, run.From, run.To)
	if err != nil {
		return Run{}, fmt.Errorf("recompute interest run %d: %w", id, err)
	}
	if err := stale(run, fresh); err != nil {
		return Run{}, err
	}

	entries := make([]ledger.Entry, 0, len(run.Lines))
	for _, line := range run.Lines {
		entries = append(entries, entryFor(run, line, by))
	}
	if _, err := s.ledger.PostBatchTx(ctx, tx, entries); err != nil {
		return Run{}, fmt.Errorf("charge the lines of interest run %d: %w", id, err)
	}
	return run, nil
}

// overlapping names the posted run that covers this one's period.
//
// The refusal itself has already happened, in the UPDATE's WHERE clause. This
// exists to turn it into a sentence somebody can act on — *interest run 2
// already covers 2025-09-01 to 2025-09-30* — rather than a bare conflict that
// sends a manager looking for which of two things went wrong.
//
// It runs after the UPDATE, like every other read here, so it cannot be what
// upgrades the transaction's lock. It excludes the run being posted by id
// because that row is a draft at this point and would not match anyway; the
// exclusion is there so this function is also correct when called about a run
// that did post.
func overlapping(ctx context.Context, q queryExecer, run Run) error {
	var runNo int64
	var from, to string

	err := q.QueryRowContext(ctx,
		`SELECT run_no, period_from, period_to
		   FROM interest_runs
		  WHERE status = 'posted' AND id <> ?
		    AND period_from <= ? AND period_to >= ?
		  ORDER BY run_no
		  LIMIT 1`, run.ID, run.To, run.From).Scan(&runNo, &from, &to)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("check whether %s to %s has been charged: %w", run.From, run.To, err)
	}

	return fmt.Errorf("%w: interest run %d already covers %s to %s", ErrPeriodAlreadyCharged, runNo, from, to)
}

// stale compares the preview a manager approved against what the ledger says
// now, and refuses on any difference.
//
// It names the customer and both figures rather than saying "something
// changed", because the next thing whoever reads it will do is decide whether
// the change is expected — and a farmer's payment landing between the preview
// and the post is entirely expected.
//
// Both sides are ordered by customer id, so this is a walk rather than a
// lookup, and a customer appearing or disappearing is caught at the position
// they would have been in.
func stale(run Run, fresh []charge) error {
	for i, line := range run.Lines {
		if i >= len(fresh) {
			return fmt.Errorf("%w: %s is no longer charged anything",
				ErrPreviewStale, name(line))
		}
		switch got := fresh[i]; {
		case got.CustomerID != line.CustomerID:
			return fmt.Errorf("%w: it charges %s, and the ledger now says customer %d belongs there instead",
				ErrPreviewStale, name(line), got.CustomerID)
		case got.Base != line.Base:
			return fmt.Errorf("%w: %s was computed against %s and is now %s — recompute the run",
				ErrPreviewStale, name(line), line.Base, got.Base)
		case got.Charge != line.Charge:
			return fmt.Errorf("%w: %s was to be charged %s and would now be charged %s — recompute the run",
				ErrPreviewStale, name(line), line.Charge, got.Charge)
		}
	}
	if len(fresh) > len(run.Lines) {
		return fmt.Errorf("%w: customer %d would now be charged %s and is not on this run — recompute it",
			ErrPreviewStale, fresh[len(run.Lines)].CustomerID, fresh[len(run.Lines)].Charge)
	}
	return nil
}

// name is what to call a farmer in an error somebody reads: their code, which
// is what staff say, falling back to the id when a line has been read without
// its customer.
func name(line Line) string {
	if line.CustomerCode == "" {
		return fmt.Sprintf("customer %d", line.CustomerID)
	}
	return line.CustomerCode
}

// VoidTx undoes a posted run by reversing every entry it produced.
//
// The whole run or none of it, as with a voucher — and it matters more here,
// because a partially voided run is a set of balances that is wrong in a way
// that looks finished. One wrong rate means voiding and running it again.
//
// The status UPDATE goes first for the same reason as in PostTx, and carries
// the same benefit: a double-clicked Void matches no rows the second time, long
// before the ledger's at-most-once reversal index is reached.
func (s *Service) VoidTx(ctx context.Context, tx *sql.Tx, id int64, by Actor, reason string) (Run, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Run{}, ValidationError{Field: "reason", Message: "a void has to say why"}
	}
	if by.Username == "" {
		return Run{}, fmt.Errorf("a void must say who made it")
	}

	stamp := s.now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`UPDATE interest_runs
		    SET status = 'voided', voided_at = ?, voided_by = ?, voided_by_username = ?, void_reason = ?
		  WHERE id = ? AND status = 'posted'`,
		stamp, nullableID(by.UserID), by.Username, reason, id)
	if err != nil {
		return Run{}, fmt.Errorf("void interest run %d: %w", id, err)
	}
	if err := s.refused(ctx, tx, id, res, StatusPosted); err != nil {
		return Run{}, err
	}

	run, err := byID(ctx, tx, id)
	if err != nil {
		return Run{}, err
	}

	lineIDs := make([]int64, 0, len(run.Lines))
	for _, line := range run.Lines {
		lineIDs = append(lineIDs, line.ID)
	}
	entries, err := s.ledger.EntriesBySourceTx(ctx, tx, SourceTable, lineIDs)
	if err != nil {
		return Run{}, fmt.Errorf("find what interest run %d charged: %w", id, err)
	}

	// The document and the ledger must agree about what this run did before any
	// of it is undone. If they do not, voiding would be partial — some farmers
	// refunded, others left charged — and a partial void is worse than a
	// refused one, because it looks finished.
	var posted int
	for _, e := range entries {
		if !e.IsReversal() {
			posted++
		}
	}
	if posted != len(run.Lines) {
		return Run{}, fmt.Errorf(
			"interest run %d has %d lines but %d entries in the ledger: refusing to void half of it",
			id, len(run.Lines), posted)
	}

	for _, e := range entries {
		if e.IsReversal() {
			continue
		}
		if _, err := s.ledger.ReverseTx(ctx, tx, e.ID, by, reason); err != nil {
			return Run{}, fmt.Errorf("reverse entry %d of interest run %d: %w", e.ID, id, err)
		}
	}
	return run, nil
}

// Entries returns the ledger entries a posted run produced, oldest first,
// including the reversals that voiding it wrote.
func (s *Service) Entries(ctx context.Context, id int64) ([]ledger.Entry, error) {
	run, err := s.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if run.Status == StatusDraft {
		return nil, nil
	}

	lineIDs := make([]int64, 0, len(run.Lines))
	for _, line := range run.Lines {
		lineIDs = append(lineIDs, line.ID)
	}
	return s.ledger.EntriesBySource(ctx, SourceTable, lineIDs)
}

// entryFor is the ledger entry one line becomes when its run is posted.
//
// Principal is zero, and ledger.Entry.Validate refuses anything else on an
// interest entry — so a run that somehow computed a principal movement fails to
// post rather than writing a wrong one.
//
// The date is the end of the period rather than the day somebody clicked. A run
// for September charges September, whether it is posted on the 30th or on the
// 3rd of October; otherwise a statement cut at the 30th would be missing a
// charge it later turns out to have had.
func entryFor(run Run, line Line, by Actor) ledger.Entry {
	return ledger.Entry{
		CustomerID:  line.CustomerID,
		Date:        run.To,
		Type:        ledger.TypeInterest,
		Principal:   money.Zero,
		Interest:    line.Charge,
		SourceTable: SourceTable,
		SourceID:    line.ID,
		By:          by,
	}
}
