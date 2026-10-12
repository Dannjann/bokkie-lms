package api

import (
	"net/http"
	"strings"

	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
)

// The statements index: every farmer, with what they owed on a given day, as a
// way of getting to the one statement somebody actually wants.
//
// **Registered Authenticated, not ManagerOnly, which departs from the four
// reports.** Those are gated because they are portfolio views. This is arguably
// one too — it ranks the whole book — and the argument for opening it is that
// the person who has to tell a farmer what they owe is the encoder at the
// counter, and any single farmer's balance is already one click away on the
// customer screen. What this adds is the saving of those clicks, not access to
// a figure that was being withheld. If that judgement turns out wrong, the
// change is one argument here and one flag in the SPA's nav.ts.

// statementIndexRow is one farmer and their balance on the day asked about.
type statementIndexRow struct {
	Customer customer.Customer `json:"customer"`
	Balance  balanceView       `json:"balance"`
}

type statementIndexResponse struct {
	AsOf    string              `json:"as_of"`
	Rows    []statementIndexRow `json:"rows"`
	Total   int                 `json:"total"`
	Page    int                 `json:"page"`
	PerPage int                 `json:"per_page"`
}

func handleListStatements(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		q := r.URL.Query()

		asOf, err := asOfFrom(r, d)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "as_of must be a date, as 2026-03-02", RequestID: RequestID(ctx)})
			return
		}

		status := customer.Status(strings.TrimSpace(q.Get("status")))
		if q.Get("status") != "" && !status.Valid() {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "status must be active, inactive or all", RequestID: RequestID(ctx)})
			return
		}

		// The customer list's own query, so search behaves identically to
		// /customers — including treating a typed % as a percent sign, which
		// Step 4 went to some trouble over and which would be easy to lose by
		// writing a second WHERE clause here.
		page, err := d.Customers.List(ctx, customer.Query{
			Text:    q.Get("q"),
			Status:  status,
			Page:    atoiOr(q.Get("page"), 0),
			PerPage: atoiOr(q.Get("per_page"), 0),
		})
		if err != nil {
			d.Logger.Error("list statements", "id", RequestID(ctx), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}

		// One query for every customer's balance, not one per row. BalancesAt
		// is the same function the per-customer statement closes with, which is
		// the point: two routes computing one figure by different paths is
		// exactly the disagreement Step 6 refused to allow.
		balances, err := d.Ledger.BalancesAt(ctx, asOf)
		if err != nil {
			d.Logger.Error("list statements", "id", RequestID(ctx), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}
		byCustomer := make(map[int64]ledger.Balance, len(balances))
		for _, b := range balances {
			byCustomer[b.CustomerID] = b
		}

		rows := make([]statementIndexRow, 0, len(page.Customers))
		for _, c := range page.Customers {
			// A customer with no entries at all is absent from BalancesAt — it
			// answers "who owes something", and padding it with zeroes would
			// hide the rows that matter. Here the opposite is wanted: this is a
			// way of finding a farmer, and one who has never borrowed must
			// still be findable. The zero Balance renders as 0.00.
			rows = append(rows, statementIndexRow{
				Customer: c,
				Balance:  viewOf(byCustomer[c.ID]),
			})
		}

		writeJSON(w, http.StatusOK, statementIndexResponse{
			AsOf:    asOf.String(),
			Rows:    rows,
			Total:   page.Total,
			Page:    page.Page,
			PerPage: page.PerPage,
		})
	}
}
