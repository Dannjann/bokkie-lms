package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/Dannjann/bokkie-lms/internal/domain/advance"
	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
	"github.com/Dannjann/bokkie-lms/internal/domain/payment"
	"github.com/Dannjann/bokkie-lms/internal/domain/settlement"
)

// The customer statement: an opening balance, every entry over a period, the
// balance after each one, and a closing balance.
//
// The arithmetic is all in internal/domain/ledger. What happens here is the one
// thing the ledger is not allowed to do: turn an entry's provenance into the
// number staff say out loud. An entry knows it came from row 412 of
// advance_lines; only this layer knows that row 412 is on Ref. No. 1425,
// because only this layer is allowed to know that vouchers and receipts exist.

// statementResponse is a statement as a screen shows it.
type statementResponse struct {
	Customer customer.Customer `json:"customer"`

	From string `json:"from,omitempty"`
	To   string `json:"to"`

	Opening balanceView     `json:"opening"`
	Rows    []statementRow  `json:"rows"`
	Closing balanceView     `json:"closing"`
	Moved   movementSummary `json:"moved"`
}

// statementRow is one entry, the balance it left behind, and what document it
// came from.
type statementRow struct {
	Entry   entryView   `json:"entry"`
	Running balanceView `json:"running"`

	// Document is what a person would call the thing this entry came from —
	// "Ref. No. 1425", "OR No. 7", "Interest run 3" — or empty for an entry
	// with no document behind it, which today means an opening balance or an
	// adjustment.
	Document string `json:"document,omitempty"`

	// Particular is what the line or payment behind this entry was for, as it
	// was typed: "fertilizer", "palay delivery". Empty for a liquidation's
	// entries, whose Entry column already says interest or payment.
	Particular string `json:"particular,omitempty"`

	// Advance carries what a screen needs to edit the line behind this entry
	// in place, and to tell an edited slip's history from the slip as it now
	// reads. Absent for anything that is not an advance line.
	Advance *advanceRef `json:"advance,omitempty"`
}

type advanceRef struct {
	VoucherID int64 `json:"voucher_id"`
	LineID    int64 `json:"line_id"`
	Revision  int   `json:"revision"`
	// Superseded marks the entries of a revision that an edit replaced — the
	// original advance and its reversal — so a screen can fold them away.
	Superseded bool   `json:"superseded"`
	Editable   bool   `json:"editable"`
	EditedBy   string `json:"edited_by,omitempty"`
}

// movementSummary is what the period did, in total. It exists because the
// question a statement is usually opened to answer is "how much has this farmer
// paid this season", and counting the rows by eye is how people get that wrong.
type movementSummary struct {
	Principal string `json:"principal"`
	Interest  string `json:"interest"`
}

func handleGetStatement(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		q := r.URL.Query()

		id, err := pathID(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: err.Error(), RequestID: RequestID(ctx)})
			return
		}

		from, err := optionalDate(q.Get("from"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "from must be a date, as 2026-03-02", RequestID: RequestID(ctx)})
			return
		}
		to, err := optionalDate(q.Get("to"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "to must be a date, as 2026-03-02", RequestID: RequestID(ctx)})
			return
		}

		// The customer is read first: a statement of somebody who is not on the
		// books is a 404, not an empty statement that looks like a farmer who
		// has never borrowed.
		c, err := d.Customers.ByID(ctx, id)
		if errors.Is(err, customer.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, errorBody{
				Error: "no such customer", RequestID: RequestID(ctx)})
			return
		}
		if err != nil {
			d.Logger.Error("read a customer for a statement",
				"id", RequestID(ctx), "customer", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}

		stmt, err := d.Ledger.Statement(ctx, id, from, to)
		if errors.Is(err, ledger.ErrInvalidEntry) {
			// The only thing a caller can get wrong here: a range that runs
			// backwards. Everything else is a fault, and must not be dressed up
			// as one — this handler used to report every failure as a bad date
			// range, which sent the first person who hit a real one looking at
			// the date pickers while the actual error sat unlogged.
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "that date range does not make a statement", RequestID: RequestID(ctx)})
			return
		}
		if err != nil {
			d.Logger.Error("build a statement",
				"id", RequestID(ctx), "customer", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}

		moved, err := stmt.Movement()
		if err != nil {
			d.Logger.Error("total a statement", "id", RequestID(ctx), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}

		docs, err := documentsOf(ctx, d, stmt.Rows)
		if err != nil {
			d.Logger.Error("label the documents on a statement", "id", RequestID(ctx), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}

		response := statementResponse{
			Customer: c,
			To:       stmt.To.String(),
			Opening:  viewOf(stmt.Opening),
			Closing:  viewOf(stmt.Closing),
			Rows:     make([]statementRow, 0, len(stmt.Rows)),
			Moved: movementSummary{
				Principal: moved.Principal.String(),
				Interest:  moved.Interest.String(),
			},
		}
		if !stmt.From.IsZero() {
			response.From = stmt.From.String()
		}
		for _, row := range stmt.Rows {
			doc := docs[documentKey{row.Entry.SourceTable, row.Entry.SourceID}]
			response.Rows = append(response.Rows, statementRow{
				Entry:      entryViewsOf([]ledger.Entry{row.Entry})[0],
				Running:    viewOf(row.Running),
				Document:   doc.Label,
				Particular: doc.Particular,
				Advance:    doc.Advance,
			})
		}
		writeJSON(w, http.StatusOK, response)
	}
}

// label is a document's number as a person says it.
func label(kind string, number int64) string {
	return kind + " " + strconv.FormatInt(number, 10)
}

// documentKey identifies one source row: which table, and which row of it.
type documentKey struct {
	Table string
	ID    int64
}

// document is what a statement row says about where its entry came from.
type document struct {
	Label      string
	Particular string
	Advance    *advanceRef
}

// documentsOf turns the provenance on a page of entries into the numbers staff
// recognise and what each was for, in one query per source table rather than
// one per row.
//
// This is deliberately a lookup rather than a join. The ledger's query cannot
// join to advance_vouchers or payments without the ledger knowing those tables
// exist, which is the boundary the whole design rests on — so the entries come
// back carrying a table name and an id, and the labelling happens here, where
// both document packages are already imported.
func documentsOf(ctx context.Context, d Deps, rows []ledger.StatementRow) (map[documentKey]document, error) {
	byTable := make(map[string][]int64)
	for _, row := range rows {
		if row.Entry.SourceTable == "" || row.Entry.SourceID == 0 {
			continue
		}
		byTable[row.Entry.SourceTable] = append(byTable[row.Entry.SourceTable], row.Entry.SourceID)
	}
	if len(byTable) == 0 {
		return nil, nil
	}

	labels := make(map[documentKey]document, len(rows))

	if ids := byTable[advance.SourceTable]; len(ids) > 0 {
		lines, err := d.Advances.DocumentsForLines(ctx, ids)
		if err != nil {
			return nil, err
		}
		for lineID, line := range lines {
			labels[documentKey{advance.SourceTable, lineID}] = document{
				Label: label("Ref. No.", line.RefNo), Particular: line.Particular,
				Advance: &advanceRef{
					VoucherID: line.VoucherID, LineID: line.LineID, Revision: line.Revision,
					Superseded: line.Superseded, Editable: line.Editable, EditedBy: line.EditedBy,
				}}
		}
	}

	if ids := byTable[settlement.SourceTable]; len(ids) > 0 {
		nos, err := d.Settlements.NumbersFor(ctx, ids)
		if err != nil {
			return nil, err
		}
		for id, no := range nos {
			labels[documentKey{settlement.SourceTable, id}] = document{Label: label("Liquidation No.", no)}
		}
	}

	if ids := byTable[payment.SourceTable]; len(ids) > 0 {
		payments, err := d.Payments.DocumentsFor(ctx, ids)
		if err != nil {
			return nil, err
		}
		for paymentID, p := range payments {
			labels[documentKey{payment.SourceTable, paymentID}] = document{
				Label: label("OR No.", p.OrNo), Particular: p.Particular}
		}
	}

	return labels, nil
}
