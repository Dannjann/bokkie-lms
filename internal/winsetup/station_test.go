package winsetup

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStationRunningAndAnswering(t *testing.T) {
	now := time.Date(2026, 10, 16, 14, 0, 0, 0, time.UTC)
	v := DescribeStation(StationFacts{
		State: StationRunning, Asked: true, Answering: true,
		Port: 8080, Hostname: "bokkie", Addresses: []string{"192.168.1.20", "10.0.0.5"},
		LastBackup: now.Add(-2 * time.Hour), Now: now, Version: "v1.2.3",
	})
	if v.Tone != ToneGood || !v.CanOpen || v.Busy {
		t.Fatalf("a running station should read as good and openable: %+v", v)
	}
	want := map[string]string{
		"This computer":   "http://127.0.0.1:8080/",
		"Other computers": "http://bokkie.local:8080",
		"  or":            "http://192.168.1.20:8080",
		"Last backup":     "today at 12:00 PM",
		"Version":         "v1.2.3",
	}
	got := map[string]string{}
	for _, r := range v.Rows {
		got[r.Label] = r.Value
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
	// The window has six row slots; a seventh row would be silently dropped.
	if len(v.Rows) > 6 {
		t.Errorf("%d rows, the window draws six", len(v.Rows))
	}
}

// The fault the user reported: the program is open and the address refuses.
// The window must say so rather than show a green "running".
func TestStationBoundButRefusingIsBad(t *testing.T) {
	v := DescribeStation(StationFacts{State: StationRunning, Asked: true, Answering: false, Port: 8080})
	if v.Tone != ToneBad || !strings.Contains(v.Headline, "not answering") {
		t.Fatalf("got %+v", v)
	}
}

func TestStationFailedShowsTheReasonAndNoAddresses(t *testing.T) {
	v := DescribeStation(StationFacts{
		State: StationFailed, Port: 8080,
		Err: errors.New("listen on 0.0.0.0:8080: address already in use\nmore detail"),
	})
	if v.Tone != ToneBad || v.CanOpen {
		t.Fatalf("got %+v", v)
	}
	if !strings.Contains(v.Detail, "address already in use") || strings.Contains(v.Detail, "more detail") {
		t.Errorf("detail should carry the first line of the error only: %q", v.Detail)
	}
	for _, r := range v.Rows {
		if strings.HasPrefix(r.Value, "http") {
			t.Errorf("a station that never started offered an address: %+v", r)
		}
	}
}

func TestStationStoppingIsBusy(t *testing.T) {
	if v := DescribeStation(StationFacts{State: StationStopping}); !v.Busy || v.CanOpen {
		t.Fatalf("got %+v", v)
	}
}

func TestDescribeBackup(t *testing.T) {
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	cases := map[time.Time]string{
		{}:                       "none yet",
		now.Add(-time.Hour):      "today at 8:00 AM",
		now.Add(-20 * time.Hour): "yesterday at 1:00 PM",
		now.AddDate(0, 0, -5):    "11 Oct 2026 at 9:00 AM",
	}
	for at, want := range cases {
		if got := describeBackup(at, now); got != want {
			t.Errorf("describeBackup(%v) = %q, want %q", at, got, want)
		}
	}
}

// The report after v0.0.6: 127.0.0.1 works on the host and nothing works from
// anywhere else. A green "running" over that is the window lying.
func TestStationBlockedByTheFirewallIsNotGreen(t *testing.T) {
	v := DescribeStation(StationFacts{
		State: StationRunning, Asked: true, Answering: true, Port: 8080,
		Blocked: &Blocked{Problem: "public network", Fix: "make it Private"},
	})
	if v.Tone != ToneBad || !strings.Contains(v.Detail, "make it Private") {
		t.Fatalf("got %+v", v)
	}
}
