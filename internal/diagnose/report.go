// Package diagnose answers "why can nobody reach this?" without a support call.
//
// It exists because the first real Windows install produced
// ERR_CONNECTION_REFUSED on bokkie.local, and working out why took a developer
// reading source and a sequence of PowerShell commands typed by the person
// standing at the station. Every question asked that evening is a check in
// here, and the answers come with what to do about them.
//
// The checks take their dependencies as arguments — a resolver, an HTTP client,
// a clock's worth of addresses — so the reasoning is testable on a machine that
// is not the station, which is every machine this project has.
package diagnose

import (
	"fmt"
	"strings"
)

// State is how a check came out. There are three rather than two because
// "working as configured" and "broken" are not the only possibilities: a
// station deliberately set to serve only its own screen is neither.
type State int

const (
	OK State = iota
	// Note is a fact worth printing that is nobody's fault — a setting doing
	// exactly what it was set to do.
	Note
	// Problem is something that stops people getting to the app.
	Problem
)

func (s State) String() string {
	switch s {
	case OK:
		return "ok"
	case Note:
		return "note"
	default:
		return "problem"
	}
}

// Check is one question and its answer.
type Check struct {
	// Name is the question in the owner's words, not the implementation's:
	// "the name bokkie.local points at this computer", never "mDNS A record".
	Name  string
	State State
	// Detail is what was actually found. It is printed either way, because
	// "ok" lines are how somebody reads past the checks to the one that failed.
	Detail string
	// Fix is what to do, and is required whenever State is Problem. A check
	// that reports a fault without saying what to do about it has moved the
	// support call rather than removed it.
	Fix string
}

// Report is every check, in the order they were run.
type Report struct {
	Checks []Check
	// Reach is the address that was proved to work, for the closing line.
	Reach string
}

// Add records a check.
func (r *Report) Add(c Check) { r.Checks = append(r.Checks, c) }

// Healthy reports whether anything is actually wrong. Notes do not count:
// a station set to serve only its own screen is working as asked.
func (r Report) Healthy() bool {
	for _, c := range r.Checks {
		if c.State == Problem {
			return false
		}
	}
	return true
}

// Problems are the checks that need acting on.
func (r Report) Problems() []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.State == Problem {
			out = append(out, c)
		}
	}
	return out
}

// String is the whole report as the person at the station reads it.
func (r Report) String() string {
	var b strings.Builder
	for _, c := range r.Checks {
		mark := map[State]string{OK: "  ok  ", Note: " note ", Problem: " FAIL "}[c.State]
		fmt.Fprintf(&b, "[%s] %s\n", mark, c.Name)
		if c.Detail != "" {
			fmt.Fprintf(&b, "         %s\n", c.Detail)
		}
		if c.State == Problem && c.Fix != "" {
			for _, line := range strings.Split(c.Fix, "\n") {
				fmt.Fprintf(&b, "         -> %s\n", line)
			}
		}
	}

	b.WriteString("\n")
	switch {
	case !r.Healthy():
		fmt.Fprintf(&b, "%d thing(s) above need attention. Send the developer everything on this screen.\n",
			len(r.Problems()))
	case r.Reach != "":
		fmt.Fprintf(&b, "Everything checks out. Open this address at the station:\n    %s\n", r.Reach)
	default:
		b.WriteString("Everything checks out.\n")
	}
	return b.String()
}
