// Package advance owns the advance voucher: the document that lends money.
//
// A voucher is the legacy system's "Ref. No. 1425" header with its grid of
// lines beneath it. It is typed as a draft, posted once — which is the moment
// its lines become ledger entries — and undone only by reversing every one of
// those entries at once.
//
// This package imports internal/domain/ledger, and that direction is the
// correct one: an advance is a document that produces ledger entries, so it has
// to know the ledger exists. The ledger knows nothing about vouchers — it takes
// entries carrying a source table's name and never asks what is in that table.
package advance

// SourceTable is the provenance every ledger entry a voucher produces carries.
// It is the line's table rather than the voucher's, so an entry points at the
// row that names an amount and a customer rather than at the header above it.
const SourceTable = "advance_lines"

// Status is where a voucher is in its life. The set is closed, the database
// holds the same list in a CHECK constraint, and a trigger there allows only
// the two transitions below.
//
//	draft ──post──> posted ──void──> voided
//
// There is no un-post and no re-open. Correcting a posted voucher means voiding
// it and entering a new one, which leaves a legible trail of what actually
// happened instead of a row that has quietly been something else before.
type Status string

const (
	// StatusDraft is a voucher being typed. It has no Ref. No., it has moved no
	// money, and it can be edited or deleted outright.
	StatusDraft Status = "draft"

	// StatusPosted is a voucher whose lines are in the ledger. Nothing about it
	// can change again.
	StatusPosted Status = "posted"

	// StatusVoided is a posted voucher whose every entry has been reversed. The
	// voucher and its reversals both stay on the record: what happened is that
	// money moved and then moved back, not that it never moved.
	StatusVoided Status = "voided"
)

// Valid reports whether s is one of the three statuses.
func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusPosted, StatusVoided:
		return true
	default:
		return false
	}
}

func (s Status) String() string { return string(s) }
