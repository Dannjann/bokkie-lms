package advance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// PostTx turns a draft into money owed: it allocates the Ref. No. and writes
// one ledger entry per line, all inside the caller's transaction.
//
// **The UPDATE below must be the first statement this transaction runs.**
//
// internal/db opens transactions deferred, so a transaction that reads first
// and writes second has to upgrade its lock — and SQLite refuses an upgrade
// that would deadlock immediately, without honouring busy_timeout. That is
// exactly what made step 4's read-then-write allocator fail as SQLITE_BUSY on
// 43 of 50 concurrent writers rather than as a duplicate code. Writing first
// makes this a write transaction from its first statement, and a second poster
// then waits its turn on the busy timeout like any other writer.
//
// Everything that would normally be a precondition is therefore checked after
// the write instead: whether the voucher exists, whether it was still a draft,
// whether it has any lines. Each of those returns an error, the caller rolls
// back, and nothing happened. The order looks inverted because it is, and it is
// load-bearing.
func (s *Service) PostTx(ctx context.Context, tx *sql.Tx, id int64, by Actor) (Voucher, error) {
	if by.Username == "" {
		return Voucher{}, fmt.Errorf("a voucher must say who posted it")
	}

	// The Ref. No. is allocated inside the UPDATE, never as a read followed by
	// a write, for the reason above and with the partial unique index on ref_no
	// as the backstop. MAX ignores the NULLs that drafts carry, so the sequence
	// counts posted vouchers only and has no gaps in it.
	//
	// refno.start therefore only has an effect while nothing has been posted:
	// after that MAX wins. That is deliberate — a setting that could renumber a
	// live sequence is a footgun — and it is why the number from the legacy
	// system goes in before the first post at cutover.
	start := int64(s.station.Int(settings.RefNoStart))
	if start < 1 {
		start = 1
	}

	stamp := s.now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`UPDATE advance_vouchers
		    SET ref_no = (SELECT COALESCE(MAX(ref_no), ? - 1) + 1 FROM advance_vouchers),
		        status = 'posted', posted_at = ?, posted_by = ?, posted_by_username = ?
		  WHERE id = ? AND status = 'draft'`,
		start, stamp, nullableID(by.UserID), by.Username, id)
	if err != nil {
		return Voucher{}, fmt.Errorf("post voucher %d: %w", id, err)
	}

	// Zero rows means it was not a draft — which is also what a double-clicked
	// Post button looks like, and why it posts once.
	if err := s.refused(ctx, tx, id, res, StatusDraft); err != nil {
		return Voucher{}, err
	}

	v, err := byID(ctx, tx, id)
	if err != nil {
		return Voucher{}, err
	}
	if len(v.Lines) == 0 {
		return Voucher{}, ValidationError{Field: "lines", Message: "an advance slip with no lines cannot be posted"}
	}

	entries := make([]ledger.Entry, 0, len(v.Lines))
	for _, line := range v.Lines {
		entries = append(entries, entryFor(line, by))
	}
	if _, err := s.ledger.PostBatchTx(ctx, tx, entries); err != nil {
		return Voucher{}, fmt.Errorf("post the lines of voucher %d: %w", id, err)
	}
	return v, nil
}

// VoidTx undoes a posted voucher by reversing every entry it produced.
//
// The whole voucher or none of it. One wrong line means voiding and re-entering,
// which leaves a legible trail — this voucher was wrong, this one replaced it —
// instead of a document whose current total is a question with a non-obvious
// answer.
//
// The status UPDATE goes first for the same reason as in PostTx, and it carries
// the same benefit: a double-clicked Void matches no rows the second time, long
// before the ledger's at-most-once reversal index is reached.
func (s *Service) VoidTx(ctx context.Context, tx *sql.Tx, id int64, by Actor, reason string) (Voucher, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Voucher{}, ValidationError{Field: "reason", Message: "a void has to say why"}
	}
	if by.Username == "" {
		return Voucher{}, fmt.Errorf("a void must say who made it")
	}

	stamp := s.now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`UPDATE advance_vouchers
		    SET status = 'voided', voided_at = ?, voided_by = ?, voided_by_username = ?, void_reason = ?
		  WHERE id = ? AND status = 'posted'`,
		stamp, nullableID(by.UserID), by.Username, reason, id)
	if err != nil {
		return Voucher{}, fmt.Errorf("void voucher %d: %w", id, err)
	}
	if err := s.refused(ctx, tx, id, res, StatusPosted); err != nil {
		return Voucher{}, err
	}

	v, err := byID(ctx, tx, id)
	if err != nil {
		return Voucher{}, err
	}

	var settledBy int64
	switch err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(st.settlement_no, 0)
		   FROM settlement_advances sa
		   JOIN settlements st ON st.id = sa.settlement_id
		   JOIN advance_lines l ON l.id = sa.advance_line_id
		  WHERE l.voucher_id = ? AND st.status = 'posted'
		  LIMIT 1`, id).Scan(&settledBy); {
	case err == nil:
		return Voucher{}, fmt.Errorf("%w (liquidation %d)", ErrSettled, settledBy)
	case !errors.Is(err, sql.ErrNoRows):
		return Voucher{}, fmt.Errorf("check whether advance slip %d is settled: %w", id, err)
	}

	lineIDs := make([]int64, 0, len(v.Lines))
	for _, line := range v.Lines {
		lineIDs = append(lineIDs, line.ID)
	}
	entries, err := s.ledger.EntriesBySourceTx(ctx, tx, SourceTable, lineIDs)
	if err != nil {
		return Voucher{}, fmt.Errorf("find what voucher %d posted: %w", id, err)
	}

	// The document and the ledger must agree about what this voucher did before
	// any of it is undone. If they do not, voiding would be partial — some lines
	// reversed, others left standing — and a partial void is worse than a
	// refused one, because it looks finished.
	var posted int
	for _, e := range entries {
		if !e.IsReversal() {
			posted++
		}
	}
	if posted != len(v.Lines) {
		return Voucher{}, fmt.Errorf(
			"voucher %d has %d lines but %d entries in the ledger: refusing to void half of it",
			id, len(v.Lines), posted)
	}

	for _, e := range entries {
		if e.IsReversal() {
			continue
		}
		if _, err := s.ledger.ReverseTx(ctx, tx, e.ID, by, reason); err != nil {
			return Voucher{}, fmt.Errorf("reverse entry %d of voucher %d: %w", e.ID, id, err)
		}
	}
	return v, nil
}

// Entries returns the ledger entries a posted voucher produced, oldest first,
// including the reversals that voiding it wrote.
//
// This is what the voucher screen shows underneath a posted voucher: not a
// restatement of its lines, but what the ledger actually holds because of it.
func (s *Service) Entries(ctx context.Context, id int64) ([]ledger.Entry, error) {
	v, err := s.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if v.Status == StatusDraft {
		return nil, nil
	}

	lineIDs := make([]int64, 0, len(v.Lines))
	for _, line := range v.Lines {
		lineIDs = append(lineIDs, line.ID)
	}
	return s.ledger.EntriesBySource(ctx, SourceTable, lineIDs)
}

// entryFor is the ledger entry one line becomes when its voucher is posted.
//
// Interest is zero whatever the station's interest rule turns out to be: an
// advance is principal, and interest is a separate entry type written by a
// separate run. Provenance points at the line rather than at the voucher above
// it, so an entry names the row that carries an amount and a customer.
func entryFor(line Line, by Actor) ledger.Entry {
	return ledger.Entry{
		CustomerID:  line.CustomerID,
		Date:        line.Date,
		Type:        ledger.TypeAdvance,
		Principal:   line.Amount,
		Interest:    money.Zero,
		SourceTable: SourceTable,
		SourceID:    line.ID,
		By:          by,
	}
}
