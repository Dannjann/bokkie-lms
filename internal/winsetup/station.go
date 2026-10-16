package winsetup

import (
	"fmt"
	"time"
)

// StationState is where the station is in its one lifetime: a window opens it,
// and closing the window ends it.
type StationState int

const (
	StationStarting StationState = iota
	// StationRunning means the port was bound. Whether it is actually
	// answering is Answering, which is checked rather than assumed — a bound
	// port that refuses connections is the fault this window exists to show.
	StationRunning
	StationStopping
	StationFailed
)

// Tone is the colour the headline is drawn in.
type Tone int

const (
	ToneNeutral Tone = iota
	ToneGood
	ToneBad
)

// StationFacts is everything the station window knows, gathered off the window's
// own thread so that drawing it never waits on the network or the disk.
type StationFacts struct {
	State StationState
	// Err is why it failed to start, when State is StationFailed.
	Err error
	// Answering is whether the station's own health check answered the last
	// time it was asked, and Asked whether it has been asked at all yet.
	Answering, Asked bool

	Port      int
	Hostname  string // the LAN name without ".local"; empty when mDNS is off
	Addresses []string
	DataDir   string

	// Blocked, when set, is Windows keeping every other computer out while
	// this one is served perfectly well.
	Blocked *Blocked

	LastBackup time.Time
	Version    string
	Now        time.Time
}

// Row is one labelled line under the headline.
type Row struct{ Label, Value string }

// StationView is what the window draws. It is computed rather than assembled
// in the window code so the words can be read and tested on a machine that is
// not Windows.
type StationView struct {
	Headline string
	Detail   string
	Tone     Tone
	Rows     []Row
	// CanOpen is whether "Open Bokkie" has anything to open.
	CanOpen bool
	// Busy greys every button out while the station is going down.
	Busy bool
}

// LocalAddress is what this computer's browser is sent to. An address rather
// than "localhost", for the reason serve.go gives: a name is one more step that
// can fail in the one window somebody is watching.
func LocalAddress(port int) string { return fmt.Sprintf("http://127.0.0.1:%d/", port) }

// DescribeStation turns the facts into the window's words.
func DescribeStation(f StationFacts) StationView {
	var v StationView

	switch f.State {
	case StationStarting:
		v.Headline = "Starting Bokkie…"
		v.Detail = "Opening the records and taking a backup. This takes a few seconds."
	case StationStopping:
		v.Headline = "Stopping Bokkie…"
		v.Detail = "Letting any counter finish what it is saving, then taking a backup.\n" +
			"This window closes by itself when it is done."
		v.Busy = true
	case StationFailed:
		v.Headline = "Bokkie could not start"
		v.Tone = ToneBad
		v.Detail = "Nothing is being served. "
		if f.Err != nil {
			v.Detail += firstLine(f.Err.Error())
		}
		v.Detail += "\nPress \"Run a check\" for what to do about it."
		// Where the records are is still worth showing: it is what a person
		// on the phone to the developer is asked first.
		return withRows(v, f, false)
	case StationRunning:
		v.CanOpen = true
		switch {
		case !f.Asked:
			v.Headline = "Bokkie is starting up…"
		case f.Answering && f.Blocked != nil:
			v.Headline = "Running, but only on this computer"
			v.Tone = ToneBad
			v.Detail = f.Blocked.Problem + "\n" + f.Blocked.Fix
		case f.Answering:
			v.Headline = "Bokkie is running"
			v.Tone = ToneGood
			v.Detail = "The counter computers can use it now. Closing this window\n" +
				"stops Bokkie for every computer at the station."
		default:
			v.Headline = "Bokkie is not answering"
			v.Tone = ToneBad
			v.Detail = "The program is open but its own address refuses to connect.\n" +
				"Press \"Run a check\" to find out why."
		}
	}
	return withRows(v, f, f.State == StationRunning)
}

func withRows(v StationView, f StationFacts, serving bool) StationView {
	if serving {
		v.Rows = append(v.Rows, Row{"This computer", LocalAddress(f.Port)})
		var network []string
		if f.Hostname != "" {
			network = append(network, fmt.Sprintf("http://%s.local:%d", f.Hostname, f.Port))
		}
		for _, a := range f.Addresses {
			network = append(network, fmt.Sprintf("http://%s:%d", a, f.Port))
		}
		// Two at most: the name, and the address that works when the name
		// does not. A machine with a VPN and a virtual switch has five, and a
		// list of five is a list nobody reads.
		if len(network) > 2 {
			network = network[:2]
		}
		for i, n := range network {
			label := "Other computers"
			if i > 0 {
				label = "  or"
			}
			v.Rows = append(v.Rows, Row{label, n})
		}
		v.Rows = append(v.Rows, Row{"Last backup", describeBackup(f.LastBackup, f.Now)})
	}
	if f.DataDir != "" {
		v.Rows = append(v.Rows, Row{"Records", f.DataDir})
	}
	if f.Version != "" {
		v.Rows = append(v.Rows, Row{"Version", f.Version})
	}
	return v
}

// describeBackup says when, in the words a person checks a clock with.
func describeBackup(at, now time.Time) string {
	if at.IsZero() {
		return "none yet"
	}
	if now.IsZero() {
		now = time.Now()
	}
	at = at.In(now.Location())
	switch {
	case sameDay(at, now):
		return "today at " + at.Format("3:04 PM")
	case sameDay(at, now.AddDate(0, 0, -1)):
		return "yesterday at " + at.Format("3:04 PM")
	default:
		return at.Format("2 Jan 2006 at 3:04 PM")
	}
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
