package settlement

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/money"
	"github.com/Dannjann/bokkie-lms/internal/domain/payment"
)

// PostTx applies a settlement: it allocates the number, charges the interest,
// and applies the payments, all inside the caller's transaction.
//
// **The first statement must be a write**, for the reason every other
// document's post begins with one: internal/db opens transactions deferred,
// and a transaction that reads first and writes second has to upgrade its
// lock, which SQLite refuses without waiting. Here the first write is a claim
// that changes nothing — it makes this transaction the writer, and a second
// poster waits its turn on the busy timeout — because the recompute has to
// see the record *without* this settlement counted as posted:
//
//  1. Claim the row while it is still a draft.
//  2. Recompute from the record and refuse if anything moved since the
//     preview — including another settlement for the same farmer that posted
//     first and took these payments.
//  3. Refuse a settlement with no payment.
//  4. Number it and mark it posted.
//  5. Write the entries: one interest entry for the whole charge, then one
//     payment entry per payment, all dated the cut-off.
func (s *Service) PostTx(ctx context.Context, tx *sql.Tx, id int64, by Actor) (Settlement, error) {
	if by.Username == "" {
		return Settlement{}, fmt.Errorf("a settlement must say who posted it")
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE settlements SET status = status WHERE id = ? AND status = 'draft'`, id)
	if err != nil {
		return Settlement{}, fmt.Errorf("post settlement %d: %w", id, err)
	}
	if err := s.refused(ctx, tx, id, res, StatusDraft); err != nil {
		return Settlement{}, err
	}

	st, err := byID(ctx, tx, id)
	if err != nil {
		return Settlement{}, err
	}
	fresh, err := s.compute(ctx, tx, st.CustomerID, st.Cutoff, st.RateBps)
	if err != nil {
		return Settlement{}, err
	}
	if err := stale(st, fresh); err != nil {
		return Settlement{}, err
	}
	if len(st.Payments) == 0 {
		return Settlement{}, fmt.Errorf("%w for %s up to %s", ErrNoPayment, st.CustomerCode, st.Cutoff)
	}

	stamp := s.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`UPDATE settlements
		    SET settlement_no = (SELECT COALESCE(MAX(settlement_no), 0) + 1 FROM settlements),
		        status = 'posted', posted_at = ?, posted_by = ?, posted_by_username = ?
		  WHERE id = ? AND status = 'draft'`,
		stamp, nullableID(by.UserID), by.Username, id); err != nil {
		return Settlement{}, fmt.Errorf("post settlement %d: %w", id, err)
	}

	entries := make([]ledger.Entry, 0, len(st.Payments)+1)
	if st.Totals.InterestCharged.IsPositive() {
		entries = append(entries, ledger.Entry{
			CustomerID:  st.CustomerID,
			Date:        st.Cutoff,
			Type:        ledger.TypeInterest,
			Principal:   money.Zero,
			Interest:    st.Totals.InterestCharged,
			SourceTable: SourceTable,
			SourceID:    st.ID,
			By:          by,
		})
	}
	for _, a := range st.Payments {
		p := payment.Payment{ID: a.PaymentID, CustomerID: st.CustomerID}
		entry, err := payment.EntryFor(p, st.Cutoff, a.Principal, a.Interest, by)
		if err != nil {
			return Settlement{}, fmt.Errorf("post settlement %d: %w", id, err)
		}
		entries = append(entries, entry)
	}
	if _, err := s.ledger.PostBatchTx(ctx, tx, entries); err != nil {
		return Settlement{}, fmt.Errorf("post settlement %d: %w", id, err)
	}
	return byID(ctx, tx, id)
}

// stale compares the stored preview with a fresh computation and names the
// first difference.
func stale(st Settlement, fresh computed) error {
	c := st.Carried
	f := fresh.Carried
	if c.Principal != f.Principal || c.Interest != f.Interest || c.Days != f.Days || c.Charge != f.Charge {
		return fmt.Errorf("%w: the balance carried into it is now %s principal and %s interest — recompute it",
			ErrPreviewStale, f.Principal, f.Interest)
	}
	if len(fresh.Advances) != len(st.Advances) {
		return fmt.Errorf("%w: it charged %d advances and there are now %d open — recompute it",
			ErrPreviewStale, len(st.Advances), len(fresh.Advances))
	}
	for i, a := range st.Advances {
		g := fresh.Advances[i]
		if g.AdvanceLineID != a.AdvanceLineID || g.Principal != a.Principal || g.Days != a.Days || g.Charge != a.Charge {
			return fmt.Errorf("%w: the advance of %s on %s has changed — recompute it",
				ErrPreviewStale, a.Principal, a.Date)
		}
	}
	if len(fresh.Payments) != len(st.Payments) {
		return fmt.Errorf("%w: it applied %d payments and there are now %d waiting — recompute it",
			ErrPreviewStale, len(st.Payments), len(fresh.Payments))
	}
	for i, p := range st.Payments {
		g := fresh.Payments[i]
		if g.PaymentID != p.PaymentID || g.Principal != p.Principal || g.Interest != p.Interest {
			return fmt.Errorf("%w: OR No. %d would now divide differently — recompute it",
				ErrPreviewStale, p.OrNo)
		}
	}
	return nil
}

// VoidTx undoes a posted settlement by reversing every entry it wrote, which
// reopens its advances and payments for the next settlement.
//
// Only the farmer's latest posted settlement can be voided. The condition is
// in the UPDATE, so it is applied by the same statement that takes the lock.
func (s *Service) VoidTx(ctx context.Context, tx *sql.Tx, id int64, by Actor, reason string) (Settlement, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Settlement{}, ValidationError{Field: "reason", Message: "a void has to say why"}
	}
	if by.Username == "" {
		return Settlement{}, fmt.Errorf("a void must say who made it")
	}

	stamp := s.now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`UPDATE settlements
		    SET status = 'voided', voided_at = ?, voided_by = ?, voided_by_username = ?, void_reason = ?
		  WHERE id = ? AND status = 'posted'
		    AND NOT EXISTS (SELECT 1 FROM settlements later
		                     WHERE later.customer_id = settlements.customer_id
		                       AND later.status = 'posted' AND later.id <> settlements.id
		                       AND (later.cutoff_date > settlements.cutoff_date
		                            OR (later.cutoff_date = settlements.cutoff_date
		                                AND later.settlement_no > settlements.settlement_no)))`,
		stamp, nullableID(by.UserID), by.Username, reason, id)
	if err != nil {
		return Settlement{}, fmt.Errorf("void settlement %d: %w", id, err)
	}
	if err := s.refused(ctx, tx, id, res, StatusPosted); err != nil {
		return Settlement{}, err
	}

	st, err := byID(ctx, tx, id)
	if err != nil {
		return Settlement{}, err
	}

	standing, err := s.standingEntries(ctx, tx, st)
	if err != nil {
		return Settlement{}, err
	}

	// The document and the ledger must agree before any of it is undone: a
	// partial void looks finished and is not.
	want := len(st.Payments)
	if st.Totals.InterestCharged.IsPositive() {
		want++
	}
	if len(standing) != want {
		return Settlement{}, fmt.Errorf(
			"settlement %d should have %d entries standing in the ledger and has %d: refusing to void part of it",
			id, want, len(standing))
	}
	for _, e := range standing {
		if _, err := s.ledger.ReverseTx(ctx, tx, e.ID, by, reason); err != nil {
			return Settlement{}, fmt.Errorf("reverse entry %d of settlement %d: %w", e.ID, id, err)
		}
	}
	return st, nil
}

// standingEntries is every entry this settlement wrote that has not been
// reversed: its interest entry, and the current entry of each of its payments.
func (s *Service) standingEntries(ctx context.Context, tx *sql.Tx, st Settlement) ([]ledger.Entry, error) {
	own, err := s.ledger.EntriesBySourceTx(ctx, tx, SourceTable, []int64{st.ID})
	if err != nil {
		return nil, fmt.Errorf("find what settlement %d wrote: %w", st.ID, err)
	}
	ids := make([]int64, 0, len(st.Payments))
	for _, p := range st.Payments {
		ids = append(ids, p.PaymentID)
	}
	paid, err := s.ledger.EntriesBySourceTx(ctx, tx, payment.SourceTable, ids)
	if err != nil {
		return nil, fmt.Errorf("find the payments settlement %d applied: %w", st.ID, err)
	}
	return standing(append(own, paid...)), nil
}

// Entries returns every ledger entry a settlement wrote, and the reversals
// that voiding it added, oldest first.
func (s *Service) Entries(ctx context.Context, id int64) ([]ledger.Entry, error) {
	st, err := s.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if st.Status == StatusDraft {
		return nil, nil
	}
	own, err := s.ledger.EntriesBySource(ctx, SourceTable, []int64{id})
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(st.Payments))
	for _, p := range st.Payments {
		ids = append(ids, p.PaymentID)
	}
	paid, err := s.ledger.EntriesBySource(ctx, payment.SourceTable, ids)
	if err != nil {
		return nil, err
	}
	// A payment applied by an earlier, voided settlement has entries from that
	// one too. Only the entries on or after this settlement's cut-off, dated as
	// this settlement dates them, belong to it.
	out := own
	for _, e := range paid {
		if e.Date.Equal(st.Cutoff) || e.IsReversal() {
			out = append(out, e)
		}
	}
	return out, nil
}

// standing drops every entry that has been reversed, and every reversal.
func standing(entries []ledger.Entry) []ledger.Entry {
	reversed := map[int64]bool{}
	for _, e := range entries {
		if e.IsReversal() {
			reversed[e.ReversesID] = true
		}
	}
	var out []ledger.Entry
	for _, e := range entries {
		if !e.IsReversal() && !reversed[e.ID] {
			out = append(out, e)
		}
	}
	return out
}
