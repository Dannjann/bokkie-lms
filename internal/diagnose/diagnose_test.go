package diagnose

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serving stands up a fake app and hands back a client that reaches it only on
// the ports named.
//
// The dialer is the point. A plain httptest client would let these checks dial
// the real bokkie.local — which, on a developer's own network, may well answer
// — and the test would be measuring the room rather than the code.
func serving(t *testing.T, openPorts ...string) (*http.Client, func(string) ([]string, error)) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(server.Close)
	target := server.Listener.Addr().String()

	open := map[string]bool{}
	for _, p := range openPorts {
		open[p] = true
	}

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if !open[port] {
				return nil, &net.OpError{Op: "dial", Net: network, Err: errors.New("connection refused")}
			}
			return (&net.Dialer{}).DialContext(ctx, network, target)
		},
	}}
	return client, func(string) ([]string, error) { return []string{"192.168.1.50"}, nil }
}

// Nothing below the first check can mean anything while the app is down, and a
// report naming four faults sends somebody chasing four when there is one.
func TestNothingElseIsCheckedWhileTheAppIsDown(t *testing.T) {
	r := Run(Options{
		Hostname: "bokkie", Port: 8080,
		ServingErr: errors.New("connection refused"),
		StartHint:  "sc start bokkie",
		LogDir:     `C:\ProgramData\Bokkie\logs`,
	})

	if len(r.Checks) != 1 {
		t.Fatalf("ran %d checks with the app down, want 1:\n%s", len(r.Checks), r)
	}
	if r.Healthy() {
		t.Error("a station with nothing running was reported healthy")
	}
	// The hint is the caller's, because it differs per platform — a report
	// telling a Mac to run `sc start bokkie` is worse than one saying nothing.
	for _, want := range []string{"sc start bokkie", `C:\ProgramData\Bokkie\logs`} {
		if !strings.Contains(r.Checks[0].Fix, want) {
			t.Errorf("the fix is missing %q:\n%s", want, r.Checks[0].Fix)
		}
	}
}

// The switch is a setting doing what it was set to do. Reporting it as a fault
// sends somebody looking for a bug instead of for the switch.
func TestHostOnlyIsANoteAndNotAFault(t *testing.T) {
	client, _ := serving(t, "8080")
	r := Run(Options{
		Hostname: "bokkie", Port: 8080, Serving: "v0.0.1",
		LANAccessKnown: true, LANAccess: false,
		LookupHost:     func(string) ([]string, error) { return nil, errors.New("no such host") },
		LocalAddresses: []string{"192.168.1.50"},
		Client:         client,
	})

	if !r.Healthy() {
		t.Errorf("a station set to host-only was reported as broken:\n%s", r)
	}
}

// The failure that looks exactly like a name that does not resolve, and needs
// the opposite fix.
func TestANamePointingAtAnotherMachineIsSaidSoPlainly(t *testing.T) {
	client, _ := serving(t, "8080")
	r := Run(Options{
		Hostname: "bokkie", Port: 8080, Serving: "v0.0.1",
		LANAccessKnown: true, LANAccess: true,
		LookupHost:     func(string) ([]string, error) { return []string{"192.168.1.99"}, nil },
		LocalAddresses: []string{"192.168.1.50"},
		Client:         client,
	})

	if r.Healthy() {
		t.Fatalf("a name resolving to another machine was reported healthy:\n%s", r)
	}
	found := false
	for _, c := range r.Problems() {
		if strings.Contains(c.Detail, "192.168.1.99") && strings.Contains(c.Detail, "192.168.1.50") {
			found = true
		}
	}
	if !found {
		t.Errorf("the report does not show both addresses, which is the whole point:\n%s", r)
	}
}

// Every problem must carry what to do about it. A diagnostic that reports a
// fault without a next step has moved the support call, not removed it.
func TestEveryProblemCarriesAFix(t *testing.T) {
	cases := map[string]Options{
		"app down":        {Hostname: "bokkie", Port: 8080, ServingErr: errors.New("refused")},
		"name elsewhere":  {Hostname: "bokkie", Port: 8080, LANAccessKnown: true, LANAccess: true, LocalAddresses: []string{"192.168.1.50"}, LookupHost: func(string) ([]string, error) { return []string{"10.0.0.1"}, nil }},
		"name unresolved": {Hostname: "bokkie", Port: 8080, LANAccessKnown: true, LANAccess: true, LocalAddresses: []string{"192.168.1.50"}, LookupHost: func(string) ([]string, error) { return nil, errors.New("no such host") }},
	}
	for name, opts := range cases {
		for _, c := range Run(opts).Problems() {
			if strings.TrimSpace(c.Fix) == "" {
				t.Errorf("%s: %q reports a fault with nothing to do about it", name, c.Name)
			}
		}
	}
}

// The missing port number is the thing that started all this, and it is not a
// fault — it is the reason a working station looks broken.
func TestTheShortAddressIsANoteWhenNothingIsOnPort80(t *testing.T) {
	client, lookup := serving(t, "8080") // 80 deliberately closed
	r := Run(Options{
		Hostname: "bokkie", Port: 8080, Serving: "v0.0.1",
		LANAccessKnown: true, LANAccess: true,
		LookupHost: lookup, LocalAddresses: []string{"192.168.1.50"},
		Client: client,
	})

	// A station reachable on its real address is not broken because the short
	// form does not work. Grading this a fault would put a FAIL on every
	// healthy station that has not been upgraded.
	if !r.Healthy() {
		t.Errorf("a station answering on its port was reported broken:\n%s", r)
	}
	if r.Reach != "http://bokkie.local:8080" {
		t.Errorf("Reach = %q, want the address that actually answered", r.Reach)
	}
}

// With port 80 open the short address is what the person should be given,
// because it is the one they will type.
func TestTheShortAddressWinsWhenItWorks(t *testing.T) {
	client, lookup := serving(t, "8080", "80")
	r := Run(Options{
		Hostname: "bokkie", Port: 8080, Serving: "v0.0.1",
		LANAccessKnown: true, LANAccess: true,
		LookupHost: lookup, LocalAddresses: []string{"192.168.1.50"},
		Client: client,
	})
	if r.Reach != "http://bokkie.local" {
		t.Errorf("Reach = %q, want the short address once it answers", r.Reach)
	}
}

func TestTheReportNamesAnAddressThatWorks(t *testing.T) {
	client, lookup := serving(t, "8080")
	r := Run(Options{
		Hostname: "bokkie", Port: 8080, Serving: "v0.0.1",
		LANAccessKnown: true, LANAccess: true,
		LookupHost: lookup, LocalAddresses: []string{"192.168.1.50"},
		Client: client,
	})
	if r.Reach == "" {
		t.Error("a healthy report named no address to open")
	}
	if !strings.Contains(r.String(), r.Reach) {
		t.Errorf("the closing line does not name the address:\n%s", r)
	}
}
