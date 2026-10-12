package diagnose

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Options is everything the checks need from the outside world. They are
// fields rather than package-level calls so a test can hand over a resolver
// that answers whatever the test is about.
type Options struct {
	Hostname string // "bokkie", without .local
	Port     int

	// Serving is the version answering on 127.0.0.1, and the error if nothing
	// is. Taken from the caller because service.ServingVersion already exists
	// and this package has no business importing the service manager.
	Serving    string
	ServingErr error

	// LANAccess is the setting, and LANAccessKnown says whether it could be
	// read at all — an installed station whose database cannot be opened is a
	// different story from one set to host-only.
	LANAccess      bool
	LANAccessKnown bool

	// LookupHost resolves a name. nil means net.LookupHost.
	LookupHost func(string) ([]string, error)
	// LocalAddresses are this machine's own IPv4 addresses.
	LocalAddresses []string
	// Client fetches a URL. nil means a short-timeout client.
	Client *http.Client

	// StartHint is the command that starts the service on this host, and
	// LogDir is where its log is. They are passed in because they differ per
	// platform and this package has no business knowing which one it is on —
	// and because a report that tells a Mac to run `sc start bokkie` is worse
	// than one that says nothing.
	StartHint string
	LogDir    string
}

func (o Options) lookup(name string) ([]string, error) {
	if o.LookupHost != nil {
		return o.LookupHost(name)
	}
	return net.LookupHost(name)
}

func (o Options) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return &http.Client{Timeout: 3 * time.Second}
}

// Name is the LAN name, as it is typed into a browser.
func (o Options) Name() string { return o.Hostname + ".local" }

// Run works through every check in order and returns what it found.
//
// The order matters: each check is only meaningful if the ones above it
// passed, and a report that says the name does not resolve *and* the app does
// not answer sends somebody chasing two faults when there is one.
func Run(opts Options) Report {
	var r Report

	if !checkServing(&r, opts) {
		// Nothing below can mean anything while the app itself is down.
		return r
	}
	checkLANAccess(&r, opts)
	addresses := checkName(&r, opts)
	checkNameReachable(&r, opts, addresses)
	checkBarePort(&r, opts)
	return r
}

// checkServing is the first question: is the app running on this machine at
// all? Everything else is downstream of it.
func checkServing(r *Report, opts Options) bool {
	local := fmt.Sprintf("http://localhost:%d", opts.Port)
	if opts.ServingErr != nil {
		fix := "Bokkie is installed but not answering."
		if opts.StartHint != "" {
			fix += "\nStart it:  " + opts.StartHint
		}
		if opts.LogDir != "" {
			fix += "\nIf that does not work, the log says why:\n  " + opts.LogDir
		}
		r.Add(Check{
			Name:   "Bokkie is running on this computer",
			State:  Problem,
			Detail: fmt.Sprintf("nothing answered at %s: %v", local, opts.ServingErr),
			Fix:    fix,
		})
		return false
	}
	r.Add(Check{
		Name:   "Bokkie is running on this computer",
		State:  OK,
		Detail: fmt.Sprintf("%s answered, version %s", local, opts.Serving),
	})
	r.Reach = local
	return true
}

// checkLANAccess reports the switch. It is a Note and never a Problem: a
// station deliberately set to serve only its own screen is working as asked,
// and calling that a fault would send somebody looking for a bug instead of
// for the setting.
func checkLANAccess(r *Report, opts Options) {
	switch {
	case !opts.LANAccessKnown:
		r.Add(Check{
			Name:   "The other computers are allowed in",
			State:  Note,
			Detail: "could not read the setting; skipping",
		})
	case opts.LANAccess:
		r.Add(Check{Name: "The other computers are allowed in", State: OK,
			Detail: "network access is on"})
	default:
		r.Add(Check{
			Name:   "The other computers are allowed in",
			State:  Note,
			Detail: "network access is set to this machine only, so the name is not advertised and the counter PCs are refused",
		})
	}
}

// checkName asks whether the LAN name points at this machine. A name that
// resolves somewhere else is the failure that looks exactly like a name that
// does not resolve at all, and the two need opposite fixes.
func checkName(r *Report, opts Options) []string {
	name := opts.Name()

	addresses, err := opts.lookup(name)
	if err != nil || len(addresses) == 0 {
		state, fix := Problem, "Try the name from a counter PC or a phone before assuming it is broken.\n"+
			"Some host computers cannot look up their own .local name even when\n"+
			"every other machine on the network can.\n"+
			"\n"+
			"If it does not work there either, use this computer's address instead:\n"+
			"  http://"+firstOr(opts.LocalAddresses, "<this computer's IP>")+":"+strconv.Itoa(opts.Port)+"\n"+
			"On the counter PCs, run BokkieSetup.exe and choose the shortcut option,\n"+
			"then give it that address when it asks."
		if opts.LANAccessKnown && !opts.LANAccess {
			// Withdrawn on purpose. Saying "FAIL" here would be reporting a
			// setting as a fault.
			state, fix = Note, ""
		}
		r.Add(Check{
			Name:   "The name " + name + " points at this computer",
			State:  state,
			Detail: fmt.Sprintf("%s did not resolve (%v)", name, err),
			Fix:    fix,
		})
		return nil
	}

	if shared := intersect(addresses, opts.LocalAddresses); len(shared) == 0 && len(opts.LocalAddresses) > 0 {
		r.Add(Check{
			Name:  "The name " + name + " points at this computer",
			State: Problem,
			Detail: fmt.Sprintf("%s resolves to %s, but this computer is %s",
				name, strings.Join(addresses, ", "), strings.Join(opts.LocalAddresses, ", ")),
			Fix: "Another machine on this network is answering to that name.\n" +
				"Use this computer's address instead:  http://" +
				firstOr(opts.LocalAddresses, "<this computer's IP>") + ":" + strconv.Itoa(opts.Port),
		})
		return addresses
	}

	r.Add(Check{
		Name:   "The name " + name + " points at this computer",
		State:  OK,
		Detail: fmt.Sprintf("%s resolves to %s", name, strings.Join(addresses, ", ")),
	})
	return addresses
}

// checkNameReachable is the check that matters most, because it is the thing
// the person actually does: open the name and the port, and see the app.
func checkNameReachable(r *Report, opts Options, addresses []string) {
	if len(addresses) == 0 {
		return
	}
	url := fmt.Sprintf("http://%s:%d/healthz", opts.Name(), opts.Port)
	display := fmt.Sprintf("http://%s:%d", opts.Name(), opts.Port)

	if err := fetch(opts.client(), url); err != nil {
		r.Add(Check{
			Name:   "Bokkie answers at " + display,
			State:  Problem,
			Detail: err.Error(),
			Fix: "The name resolves but nothing answers on it.\n" +
				"Use this computer's address instead:  http://" +
				firstOr(opts.LocalAddresses, "<this computer's IP>") + ":" + strconv.Itoa(opts.Port),
		})
		return
	}
	r.Add(Check{Name: "Bokkie answers at " + display, State: OK})
	r.Reach = display
}

// checkBarePort is here because of the evening this package was written for.
// Somebody types bokkie.local, the browser goes to port 80, nothing is there,
// and the browser says "refused to connect" — which reads as a broken server
// rather than a missing port number.
//
// It asks localhost first and the LAN name second. Asking only the name would
// mean a host that cannot resolve its own .local name — which is common, and
// is not a fault — never finds out whether the short address works at all.
func checkBarePort(r *Report, opts Options) {
	const name = "Bokkie answers at the short address, without a port number"

	if err := fetch(opts.client(), "http://localhost/healthz"); err != nil {
		r.Add(Check{
			Name:  name,
			State: Note,
			Detail: "nothing of ours answers on port 80 (" + err.Error() + ");\n" +
				"         something else on this computer may already use it. The address\n" +
				"         needs its port number: http://" + opts.Name() + ":" + strconv.Itoa(opts.Port),
		})
		return
	}

	// It works here. Whether the *name* also reaches it is a separate question,
	// and only worth asking when the name resolves at all.
	bare := "http://" + opts.Name()
	if len(opts.LocalAddresses) > 0 {
		if err := fetch(opts.client(), bare+"/healthz"); err == nil {
			r.Add(Check{Name: name, State: OK, Detail: bare + " works, so the port number is optional"})
			r.Reach = bare
			return
		}
	}
	r.Add(Check{Name: name, State: OK, Detail: "port 80 answers on this computer"})
}

func fetch(client *http.Client, url string) error {
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("could not reach %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return nil
}

func intersect(a, b []string) []string {
	var out []string
	for _, x := range a {
		for _, y := range b {
			if x == y {
				out = append(out, x)
			}
		}
	}
	return out
}

func firstOr(values []string, fallback string) string {
	if len(values) > 0 {
		return values[0]
	}
	return fallback
}

// LocalIPv4 collects this machine's own LAN addresses, for comparing against
// whatever the name resolved to.
func LocalIPv4() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() {
			continue
		}
		if v4 := ipnet.IP.To4(); v4 != nil {
			out = append(out, v4.String())
		}
	}
	return out
}
