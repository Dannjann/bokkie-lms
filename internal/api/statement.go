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
	// "Ref. No. 1425", "OR No. 7" — or empty for an entry with no document
	// behind it, which today means an opening balance or an adjustment.
	Document string `json:"document,omitempty"`
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
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "that date range does not make a statement", RequestID: RequestID(ctx)})
			return
		}

		moved, err := stmt.Movement()
		if err != nil {
			d.Logger.Error("total a statement", "id", RequestID(ctx), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}

		labels, err := documentLabels(ctx, d, stmt.Rows)
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
			response.Rows = append(response.Rows, statementRow{
				Entry:    entryViewsOf([]ledger.Entry{row.Entry})[0],
				Running:  viewOf(row.Running),
				Document: labels[documentKey{row.Entry.SourceTable, row.Entry.SourceID}],
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

// documentLabels turns the provenance on a page of entries into the numbers
// staff recognise, in one query per source table rather than one per row.
//
// This is deliberately a lookup rather than a join. The ledger's query cannot
// join to advance_vouchers or payments without the ledger knowing those tables
// exist, which is the boundary the whole design rests on — so the entries come
// back carrying a table name and an id, and the labelling happens here, where
// both document packages are already imported.
func documentLabels(ctx context.Context, d Deps, rows []ledger.StatementRow) (map[documentKey]string, error) {
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

	labels := make(map[documentKey]string, len(rows))

	if ids := byTable[advance.SourceTable]; len(ids) > 0 {
		refNos, err := d.Advances.RefNosForLines(ctx, ids)
		if err != nil {
			return nil, err
		}
		for lineID, refNo := range refNos {
			labels[documentKey{advance.SourceTable, lineID}] = label("Ref. No.", refNo)
		}
	}

	if ids := byTable[payment.SourceTable]; len(ids) > 0 {
		orNos, err := d.Payments.OrNosFor(ctx, ids)
		if err != nil {
			return nil, err
		}
		for paymentID, orNo := range orNos {
			labels[documentKey{payment.SourceTable, paymentID}] = label("OR No.", orNo)
		}
	}

	return labels, nil
}
