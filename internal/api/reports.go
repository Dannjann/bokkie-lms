package api

import (
	"net/http"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/domain/ledger"
)

// The four reports.
//
// All read-only, all manager-only, and all shaped the same way: a date question
// in, a page of figures out. Every number on them is computed on the server and
// arrives as a decimal string — a report is the most tempting place in the
// application to total a column in JavaScript, and the one place a wrong total
// would be believed without checking.
//
// Two of them are "as at" a day and two cover a period, and the handlers keep
// those apart deliberately. A report that quietly read a date range as a
// cut-off would produce a plausible wrong number, which is the only kind worth
// fearing.

// stationToday is the station's current calendar day, which every default is built
// from. It comes from the settings rather than from the machine so that a
// report opened at 11pm in Manila is not dated tomorrow.
func stationToday(d Deps) ledger.Date {
	return ledger.DateOf(time.Now().In(d.Settings.Location()))
}

// asOfFrom reads the "as at" parameter, defaulting to today.
func asOfFrom(r *http.Request, d Deps) (ledger.Date, error) {
	asOf, err := optionalDate(r.URL.Query().Get("as_of"))
	if err != nil {
		return ledger.Date{}, err
	}
	if asOf.IsZero() {
		return stationToday(d), nil
	}
	return asOf, nil
}

// periodFrom reads a from/to pair, defaulting to the year so far — which is the
// period somebody opening a report without touching a picker almost always
// means.
func periodFrom(r *http.Request, d Deps) (from, to ledger.Date, err error) {
	q := r.URL.Query()
	if from, err = optionalDate(q.Get("from")); err != nil {
		return ledger.Date{}, ledger.Date{}, err
	}
	if to, err = optionalDate(q.Get("to")); err != nil {
		return ledger.Date{}, ledger.Date{}, err
	}

	now := stationToday(d)
	if to.IsZero() {
		to = now
	}
	if from.IsZero() {
		from = ledger.MustDate(now.Year(), time.January, 1)
	}
	return from, to, nil
}

func handleCurrentAdvances(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		asOf, err := asOfFrom(r, d)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "as_of must be a date, as 2026-03-02", RequestID: RequestID(r.Context())})
			return
		}

		report, err := d.Reports.CurrentAdvances(r.Context(), asOf)
		if err != nil {
			reportFailed(w, r, d, err, "build the current advances report")
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

func handleROI(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from, to, err := periodFrom(r, d)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "from and to must be dates, as 2026-03-02", RequestID: RequestID(r.Context())})
			return
		}
		if from.After(to) {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "that period runs backwards", RequestID: RequestID(r.Context())})
			return
		}

		report, err := d.Reports.ROI(r.Context(), from, to)
		if err != nil {
			reportFailed(w, r, d, err, "build the return on investment report")
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

func handleAging(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		asOf, err := asOfFrom(r, d)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "as_of must be a date, as 2026-03-02", RequestID: RequestID(r.Context())})
			return
		}

		report, err := d.Reports.Aging(r.Context(), asOf)
		if err != nil {
			reportFailed(w, r, d, err, "build the aging report")
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

func handleLiquidated(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from, to, err := periodFrom(r, d)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "from and to must be dates, as 2026-03-02", RequestID: RequestID(r.Context())})
			return
		}
		if from.After(to) {
			writeJSON(w, http.StatusBadRequest, errorBody{
				Error: "that period runs backwards", RequestID: RequestID(r.Context())})
			return
		}

		report, err := d.Reports.Liquidated(r.Context(), from, to)
		if err != nil {
			reportFailed(w, r, d, err, "build the liquidated report")
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

// reportFailed logs and answers 500.
//
// There is no error a caller can cause here that is not already caught above:
// the only inputs are dates, and both are validated before the report is asked
// for. So everything reaching this is a fault, and it is logged as one rather
// than dressed up as a bad request — the correction Step 6's statement handler
// needed, applied before it could happen again.
func reportFailed(w http.ResponseWriter, r *http.Request, d Deps, err error, doing string) {
	d.Logger.Error(doing, "id", RequestID(r.Context()), "error", err)
	writeJSON(w, http.StatusInternalServerError, errorBody{
		Error: "internal error", RequestID: RequestID(r.Context())})
}
