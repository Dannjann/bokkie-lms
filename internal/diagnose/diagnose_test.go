package diagnose

import (
	"context"
	"encoding/json"
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

	// The data folder is asked about first and passed, so the serving check is
	// the last one run: nothing below it is meaningful while the app is down.
	if last := r.Checks[len(r.Checks)-1]; last.Name != "Bokkie is running on this computer" {
		t.Fatalf("checks continued past a station that is down; last was %q:\n%s", last.Name, r)
	}
	if r.Healthy() {
		t.Error("a station with nothing running was reported healthy")
	}
	// The hint is the caller's, because it differs per platform — a report
	// telling a Mac to run `sc start bokkie` is worse than one saying nothing.
	serving := checkNamed(t, r, "Bokkie is running on this computer")
	for _, want := range []string{"sc start bokkie", `C:\ProgramData\Bokkie\logs`} {
		if !strings.Contains(serving.Fix, want) {
			t.Errorf("the fix is missing %q:\n%s", want, serving.Fix)
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

// The failure that wastes the most time. A browser opened at the port shows a
// page — just not Bokkie's — so every instinct says the install worked, and
// the advice for a stopped service ("start it") does nothing at all here.
func TestAPortClashIsNotReportedAsAStoppedService(t *testing.T) {
	r := Run(Options{
		Hostname: "bokkie", Port: 8080,
		ServingErr: errors.New("port http://127.0.0.1:8080/healthz is answering, but not with bokkie's health response"),
		PortOpen:   true,
		StartHint:  "sc start bokkie",
		ConfigPath: `C:\ProgramData\Bokkie\config.yaml`,
	})

	got := checkNamed(t, r, "Bokkie is running on this computer")
	if got.State != Problem {
		t.Fatalf("state = %v, want a problem", got.State)
	}
	if strings.Contains(got.Detail, "nothing answered") {
		t.Errorf("something answered, and the report said nothing did:\n%s", got.Detail)
	}
	for _, want := range []string{"Another program", "different port", "config.yaml"} {
		if !strings.Contains(got.Fix, want) {
			t.Errorf("the fix is missing %q:\n%s", want, got.Fix)
		}
	}
}

// And the reverse: with nothing on the port at all, "start it" is exactly the
// right advice and the port-clash wording would send somebody hunting a
// program that is not there.
func TestAStoppedServiceIsNotReportedAsAPortClash(t *testing.T) {
	r := Run(Options{
		Hostname: "bokkie", Port: 8080,
		ServingErr: errors.New("connection refused"),
		PortOpen:   false,
		StartHint:  "sc start bokkie",
		ConfigPath: `C:\ProgramData\Bokkie\config.yaml`,
	})

	got := checkNamed(t, r, "Bokkie is running on this computer")
	if strings.Contains(got.Fix, "Another program") {
		t.Errorf("a stopped service was reported as a port clash:\n%s", got.Fix)
	}
	if !strings.Contains(got.Fix, "sc start bokkie") {
		t.Errorf("the fix does not say how to start it:\n%s", got.Fix)
	}
}

// The screen reads these words. Numbers would mean a second copy of what they
// mean living in TypeScript, and two copies disagree the first time a state is
// added between them.
func TestStatesMarshalAsWords(t *testing.T) {
	data, err := json.Marshal(Report{Checks: []Check{
		{Name: "a", State: OK},
		{Name: "b", State: Note},
		{Name: "c", State: Problem},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"state":"ok"`, `"state":"note"`, `"state":"problem"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the report is missing %s:\n%s", want, data)
		}
	}
}

// Something left behind that starts the station anyway is the station serving
// while everybody believes it is closed — which nobody goes looking for,
// because nothing looks wrong.
func TestALeftoverAutoStartIsReportedAsAProblem(t *testing.T) {
	client, lookup := serving(t, "8080")
	base := Options{
		Hostname: "bokkie", Port: 8080, Serving: "v1.0.0",
		LANAccessKnown: true, LANAccess: true,
		LookupHost: lookup, LocalAddresses: []string{"192.168.1.50"},
		Client: client,
	}

	clean := Run(base)
	if !clean.Healthy() {
		t.Errorf("a station with nothing left behind was reported unhealthy:\n%s", clean)
	}

	withLeftover := base
	withLeftover.AutoStart = []string{"service (the bokkie Windows service)"}
	got := Run(withLeftover)
	if got.Healthy() {
		t.Fatalf("a station that starts itself was reported healthy:\n%s", got)
	}
	found := false
	for _, c := range got.Problems() {
		if strings.Contains(c.Detail, "bokkie Windows service") && strings.Contains(c.Fix, "Remove the old Bokkie service") {
			found = true
		}
	}
	if !found {
		t.Errorf("the report does not name the leftover or what to do about it:\n%s", got)
	}
}

// checkNamed finds a check by its name. Position stopped meaning anything once
// the data-folder question moved to the front, and a test that indexes into
// the list breaks every time a check is added ahead of it.
func checkNamed(t *testing.T, r Report, name string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in:\n%s", name, r)
	return Check{}
}

// A station whose folder cannot be used never starts, and a report that said
// only "nothing answered" described the symptom and named nothing about the
// cause — which is what sent somebody looking at the network for a problem
// that was a folder permission.
func TestAnUnusableDataFolderIsTheOnlyThingReported(t *testing.T) {
	r := Run(Options{
		Hostname: "bokkie", Port: 8080,
		DataDirErr: errors.New("Bokkie found its records in C:\\ProgramData\\Bokkie but cannot write there. Run icacls"),
		ServingErr: errors.New("connection refused"),
	})

	if len(r.Checks) != 1 {
		t.Fatalf("ran %d checks with an unusable data folder, want only the first:\n%s", len(r.Checks), r)
	}
	if r.Healthy() {
		t.Error("a station that cannot read its own records was reported healthy")
	}
	for _, want := range []string{"ProgramData", "icacls"} {
		if !strings.Contains(r.Checks[0].Fix, want) {
			t.Errorf("the fix is missing %q:\n%s", want, r.Checks[0].Fix)
		}
	}
}

func TestFirewallBlockIsAProblemWithAFix(t *testing.T) {
	var r Report
	checkFirewall(&r, Options{FirewallChecked: true, FirewallProblem: "public network", FirewallFix: "make it Private"})
	if len(r.Checks) != 1 || r.Checks[0].State != Problem || r.Checks[0].Fix == "" {
		t.Fatalf("got %+v", r.Checks)
	}
	r = Report{}
	checkFirewall(&r, Options{})
	if len(r.Checks) != 0 {
		t.Fatalf("an unasked question must print nothing: %+v", r.Checks)
	}
}
