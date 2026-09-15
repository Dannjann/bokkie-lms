package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Dannjann/bokkie-lms/internal/domain/customer"
	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
)

// handleListCustomers is the search behind the customers screen.
//
// It is also the query Step 5's voucher-line picker and Step 8's reports will
// reuse, which is why the filtering lives in the domain package and this
// handler only translates query strings into a customer.Query.
func handleListCustomers(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		status := customer.Status(strings.TrimSpace(q.Get("status")))
		if q.Get("status") != "" && !status.Valid() {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error:     "status must be active, inactive or all",
				RequestID: RequestID(r.Context()),
			})
			return
		}

		page, err := d.Customers.List(r.Context(), customer.Query{
			Text:    q.Get("q"),
			Status:  status,
			Page:    atoiOr(q.Get("page"), 0),
			PerPage: atoiOr(q.Get("per_page"), 0),
		})
		if err != nil {
			d.Logger.Error("list customers", "id", RequestID(r.Context()), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(r.Context())})
			return
		}
		writeJSON(w, http.StatusOK, page)
	}
}

// customerDetail is one customer plus what they owe.
//
// The two halves are composed here rather than in either domain package. That
// is the whole reason this type exists: internal/domain/customer knows nothing
// about money and internal/domain/ledger knows nothing but entries and
// balances, and joining them at the API layer is what keeps both true.
type customerDetail struct {
	Customer customer.Customer `json:"customer"`
	Balance  balanceView       `json:"balance"`
}

// balanceView is a ledger balance as the screen shows it. Money marshals as a
// decimal string, so JavaScript's float arithmetic never touches a peso.
type balanceView struct {
	Principal    string `json:"principal"`
	Interest     string `json:"interest"`
	Total        string `json:"total"`
	LastActivity string `json:"last_activity,omitempty"`
}

func handleGetCustomer(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		id, err := pathID(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: err.Error(), RequestID: RequestID(ctx)})
			return
		}

		c, err := d.Customers.ByID(ctx, id)
		if errors.Is(err, customer.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, errorBody{
				Error: "no such customer", RequestID: RequestID(ctx)})
			return
		}
		if err != nil {
			d.Logger.Error("load customer", "id", RequestID(ctx), "customer", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}

		balance, err := d.Ledger.BalanceFor(ctx, id)
		if err != nil {
			d.Logger.Error("load balance", "id", RequestID(ctx), "customer", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}

		writeJSON(w, http.StatusOK, customerDetail{Customer: c, Balance: viewOf(balance)})
	}
}

func viewOf(b ledger.Balance) balanceView {
	view := balanceView{
		Principal: b.Principal.String(),
		Interest:  b.Interest.String(),
	}
	if total, err := b.Total(); err == nil {
		view.Total = total.String()
	}
	if !b.LastActivity.IsZero() {
		view.LastActivity = b.LastActivity.String()
	}
	return view
}

// handleCreateCustomer adds a farmer to the master.
//
// Open to encoders: adding somebody at the counter is the everyday work this
// system exists for, and routing it through a manager would mean the counter
// stops whenever one is not in the building.
//
// The request body is a customer.Draft, which has no code field — so a caller
// cannot supply one even by trying, and the "assigned by the server, never
// editable" rule holds at the edge of the system rather than only inside it.
func handleCreateCustomer(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		var draft customer.Draft
		if err := decodeJSON(r, &draft); err != nil {
			return Change{}, err
		}

		c, err := d.Customers.CreateTx(ctx, tx, draft)
		if err != nil {
			if se, ok := asCustomerError(err); ok {
				return Change{}, se
			}
			return Change{}, err
		}

		return Change{
			EntityID: c.Code,
			After:    c,
			Status:   http.StatusCreated,
			Body:     c,
		}, nil
	}
}

// handleUpdateCustomer changes the editable fields. The before and after both
// go into the audit entry, which is what makes "who changed this phone number
// and what was it before" answerable.
func handleUpdateCustomer(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := pathID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		var draft customer.Draft
		if err := decodeJSON(r, &draft); err != nil {
			return Change{}, err
		}

		before, err := d.Customers.ByID(ctx, id)
		if err != nil {
			if se, ok := asCustomerError(err); ok {
				return Change{}, se
			}
			return Change{}, err
		}

		after, err := d.Customers.UpdateTx(ctx, tx, id, draft)
		if err != nil {
			if se, ok := asCustomerError(err); ok {
				return Change{}, se
			}
			return Change{}, err
		}

		return Change{EntityID: after.Code, Before: before, After: after, Body: after}, nil
	}
}

type setActiveRequest struct {
	Active bool `json:"active"`
}

// handleSetCustomerActive deactivates or reactivates a customer.
//
// Manager-only. Nothing is ever deleted here — codes are allocated as a
// high-water mark, so releasing one would hand a former customer's code to a
// new one and two farmers would hold the same code on paper months apart — and
// deactivation is therefore the nearest thing to a deletion this system has.
func handleSetCustomerActive(d Deps) TxHandler {
	return func(ctx context.Context, tx *sql.Tx, r *http.Request) (Change, error) {
		id, err := pathID(r)
		if err != nil {
			return Change{}, badRequest("%s", err.Error())
		}

		var req setActiveRequest
		if err := decodeJSON(r, &req); err != nil {
			return Change{}, err
		}

		before, err := d.Customers.ByID(ctx, id)
		if err != nil {
			if se, ok := asCustomerError(err); ok {
				return Change{}, se
			}
			return Change{}, err
		}

		after, err := d.Customers.SetActiveTx(ctx, tx, id, req.Active)
		if err != nil {
			if se, ok := asCustomerError(err); ok {
				return Change{}, se
			}
			return Change{}, err
		}

		return Change{EntityID: after.Code, Before: before, After: after, Body: after}, nil
	}
}

// maxImportUpload caps the multipart body. The domain package caps what it will
// parse as well; this one stops the bytes arriving in the first place.
const maxImportUpload = customer.MaxImportBytes

// handleImportCustomers takes a CSV, either as a preview or for real.
//
// This is the only endpoint in the system that is not JSON in, and it is worth
// keeping it the only one. It writes its own audit entry — or rather, the
// domain package writes it inside the import's own transaction — because the
// route wrapper cannot tell a preview from a real import, and auditing a dry
// run as though it had changed something would be a lie in the one table that
// exists to be trusted.
func handleImportCustomers(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		user, ok := CurrentUser(ctx)
		if !ok {
			unauthorized(w, r)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxImportUpload)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error:     "could not read the uploaded file: " + err.Error(),
				RequestID: RequestID(ctx),
			})
			return
		}
		defer r.MultipartForm.RemoveAll()

		file, header, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "attach the CSV file as \"file\"", RequestID: RequestID(ctx)})
			return
		}
		defer file.Close()

		existing, err := d.Customers.ExistingCodes(ctx)
		if err != nil {
			d.Logger.Error("import: read existing codes", "id", RequestID(ctx), "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error: "internal error", RequestID: RequestID(ctx)})
			return
		}

		report, err := customer.Plan(file, existing)
		if err != nil {
			// The file could not be understood at all. That is something the
			// person who chose it can fix, so they are told what it was.
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: err.Error(), RequestID: RequestID(ctx)})
			return
		}
		report.FileName = header.Filename

		// A dry run stops here, having touched nothing. That is what makes the
		// preview worth trusting: it is not a description of the import, it is
		// the import minus the commit.
		if isDryRun(r) {
			writeJSON(w, http.StatusOK, report)
			return
		}

		if !report.OK() {
			writeJSON(w, http.StatusUnprocessableEntity, report)
			return
		}

		applied, err := d.Customers.Apply(ctx, report,
			customer.Actor{UserID: user.ID, Username: user.Username})
		if err != nil {
			d.Logger.Error("import customers", "id", RequestID(ctx),
				"user", user.Username, "file", header.Filename, "error", err)
			writeJSON(w, http.StatusInternalServerError, errorBody{
				Error:     "the import could not be completed; nothing was written",
				RequestID: RequestID(ctx)})
			return
		}

		d.Logger.Info("customers imported",
			"id", RequestID(ctx), "user", user.Username,
			"file", header.Filename, "count", applied.Accepted, "sha256", applied.SHA256)

		writeJSON(w, http.StatusOK, applied)
	}
}

// isDryRun reads the preview flag from either the query string or the form, so
// the caller can send it whichever way is convenient.
func isDryRun(r *http.Request) bool {
	for _, v := range []string{r.URL.Query().Get("dry_run"), r.FormValue("dry_run")} {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes":
			return true
		}
	}
	return false
}

// pathID reads the {id} wildcard.
func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("that is not a customer id")
	}
	return id, nil
}

func atoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

// asCustomerError maps a customer package error onto a response. Validation
// complaints are shown verbatim — they were written for the person at the
// counter — and anything unrecognised stays a fault.
func asCustomerError(err error) (statusError, bool) {
	var ve customer.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest("%s", ve.Message), true
	case errors.Is(err, customer.ErrNotFound):
		return statusError{http.StatusNotFound, "no such customer"}, true
	case errors.Is(err, customer.ErrCodeTaken):
		return conflict("%s", err.Error()), true
	case errors.Is(err, customer.ErrCodeSpaceExhausted):
		return conflict("%s", err.Error()), true
	default:
		return statusError{}, false
	}
}

// auditImportRoute is the reason the import endpoint audits itself, recorded on
// the route so the coverage test prints it.
const auditImportRoute = "a dry run and a real import arrive at the same endpoint and only one of " +
	"them changes anything; the wrapper cannot tell them apart, and the entry is written inside " +
	"the import's own transaction alongside the rows"
