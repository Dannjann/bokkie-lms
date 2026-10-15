package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Dannjann/bokkie-lms/internal/buildinfo"
	"github.com/Dannjann/bokkie-lms/internal/diagnose"
	"github.com/Dannjann/bokkie-lms/internal/settings"
)

// Station is what the control surface needs from the running app.
//
// It is an interface because internal/app imports internal/api, so the arrow
// cannot point back. Keeping it to four methods is also what stops the control
// screens growing into a second way to drive the lending system.
type Station interface {
	// LastBackup is when the most recent backup was taken, and its file name.
	LastBackup() (time.Time, string)
	// Port is where the app is listening.
	Port() int
	// Hostname is the LAN name without ".local".
	Hostname() string
	// RequestStop asks the process to shut the station down.
	RequestStop()
}

// controlPrefix is the one path the control surface answers on.
const controlPrefix = "/api/control/"

// withControl answers the control routes and passes everything else on.
//
// It sits outside the session and network middleware on purpose, which is the
// one thing about this file worth reading twice:
//
//   - Outside the session, because the person who opens the station's window
//     has not logged in yet and should not have to in order to read whether it
//     is serving. These routes expose no ledger data.
//   - Outside the network gate, because that gate answers "may the counter PCs
//     work today". This answers "may that machine switch the station off", and
//     the answer is no even on a day when the first is yes.
//
// They are logged rather than audited. The audit log records what a *person*
// did to the books, and there is deliberately no session here to attribute
// anything to — an entry with no actor would be worse than the log line.
func withControl(d Deps, next http.Handler) http.Handler {
	control := controlMux(d)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, controlPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		// A mutating endpoint with no session behind it is reachable by any
		// page the operator happens to open on this machine: a cross-origin
		// form post to localhost would stop the station. Browsers label such
		// requests, and the ones this screen makes are same-origin.
		if r.Method != http.MethodGet && crossSite(r) {
			d.Logger.Info("refused a control request from another site",
				"id", RequestID(r.Context()),
				"path", r.URL.Path,
				"sec_fetch_site", r.Header.Get("Sec-Fetch-Site"))
			writeJSON(w, http.StatusForbidden, errorBody{
				Error:     "this request did not come from the station's own screen",
				RequestID: RequestID(r.Context()),
			})
			return
		}
		if !isHostRequest(r) {
			d.Logger.Info("refused a control request from outside the host",
				"id", RequestID(r.Context()),
				"path", r.URL.Path,
				"remote", clientIP(r))
			refuseNonHost(w, r)
			return
		}
		control.ServeHTTP(w, r)
	})
}

func controlMux(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+controlPrefix+"status", d.handleControlStatus)
	mux.HandleFunc("POST "+controlPrefix+"stop", d.handleControlStop)
	mux.HandleFunc("GET "+controlPrefix+"check", d.handleControlCheck)
	return mux
}

// statusBody is the Status screen's whole contract. Its field names are
// consumed verbatim by web/src/api/control.ts, and a disagreement between the
// two is a blank screen rather than a compile error.
type statusBody struct {
	Serving bool `json:"serving"`
	// Address is this machine's own, which always works. LANAddress is the
	// name the counter PCs use, which is the one that can be wrong — they are
	// reported separately so the screen can offer the second without implying
	// the first depends on it.
	Address    string `json:"address"`
	LANAddress string `json:"lanAddress"`
	Version    string `json:"version"`
	// LastBackup is RFC3339, or null when there has never been one. Null rather
	// than a zero time: "none yet" and "1 January year 1" read very differently
	// to somebody checking whether their records are safe.
	LastBackup     *string `json:"lastBackup"`
	LastBackupFile string  `json:"lastBackupFile"`
	Name           string  `json:"name"`
}

func (d Deps) handleControlStatus(w http.ResponseWriter, r *http.Request) {
	at, file := d.Station.LastBackup()

	var last *string
	if !at.IsZero() {
		formatted := at.Format(time.RFC3339)
		last = &formatted
	}

	writeJSON(w, http.StatusOK, statusBody{
		// It answered, which is the only proof of serving that counts.
		Serving:        true,
		Address:        fmt.Sprintf("http://localhost:%d", d.Station.Port()),
		LANAddress:     fmt.Sprintf("http://%s.local:%d", d.Station.Hostname(), d.Station.Port()),
		Version:        buildinfo.Version,
		LastBackup:     last,
		LastBackupFile: file,
		Name:           d.Station.Hostname(),
	})
}

// handleControlStop answers before it stops, so the screen can say what is
// happening rather than losing the connection mid-request and showing a browser
// error page to somebody who pressed the button on purpose.
func (d Deps) handleControlStop(w http.ResponseWriter, r *http.Request) {
	d.Logger.Info("the station was stopped from its own window", "id", RequestID(r.Context()))
	writeJSON(w, http.StatusAccepted, map[string]string{
		"stopping": "the station is shutting down; the counter PCs will lose it until the window is opened again",
	})

	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	go d.Station.RequestStop()
}

// handleControlCheck runs the same checks `bokkie doctor` prints, so there is
// one set of them and one set of words.
func (d Deps) handleControlCheck(w http.ResponseWriter, r *http.Request) {
	// The real setting, read from the same store withNetworkGate reads. It was
	// hardcoded true, which meant the screen told somebody "the other computers
	// are allowed in — ok" on the one fault that explains why they are not, and
	// then reported the withdrawn LAN name as a problem with a fix telling them
	// to rebuild every counter PC's shortcut.
	lan, lanKnown := false, false
	if d.Settings != nil {
		lan, lanKnown = d.Settings.Bool(settings.LANAccess), true
	}

	report := diagnose.Run(diagnose.Options{
		Hostname:       d.Station.Hostname(),
		Port:           d.Station.Port(),
		Serving:        buildinfo.Version,
		PortOpen:       true,
		LANAccess:      lan,
		LANAccessKnown: lanKnown,
		LocalAddresses: diagnose.LocalIPv4(),
	})
	writeJSON(w, http.StatusOK, report)
}

// crossSite reports whether a request came from another site.
//
// Sec-Fetch-Site is sent by every browser this station will meet and cannot be
// set by page script. Its absence is treated as same-site rather than
// cross-site: curl and the Go test client send nothing, and refusing those
// would break the operator's own command line to defend against a browser.
func crossSite(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return false
	default:
		return true
	}
}
