//go:build darwin

package discovery

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// dnssd is Apple's client for mDNSResponder. It is part of the base system, so
// this is not a dependency anyone has to install.
const dnssd = "/usr/bin/dns-sd"

// registrationTimeout bounds how long we wait for mDNSResponder to confirm the
// name. It is generous because the responder probes the network for a conflict
// before accepting a name.
const registrationTimeout = 10 * time.Second

// advertise registers through mDNSResponder rather than running a responder of
// our own.
//
// macOS always runs mDNSResponder and it owns port 5353. A second responder
// binds without error and then answers nothing — which is precisely what
// happened on the first install: bokkie logged that it was advertising, and no
// phone or PC on the station could resolve the name. Registering through the
// responder that already owns mDNS is the same answer as the Avahi path on
// Linux, and for the same reason.
//
// The cost is a child process: the Bonjour API is C, and using it would mean
// cgo, which would end cross-compilation for every other target (Decision 5 in
// docs/plans/00-architecture.md). A supervised `dns-sd` is the cheaper trade.
func advertise(log *slog.Logger, hostname string, port int, addresses []string) (io.Closer, error) {
	address := primaryAddress(addresses)
	fqdn := hostname + ".local"

	// dns-sd -P registers a *proxy* host: both the service record and the A
	// record for the name, which is the half that matters for the shortcut on
	// each counter PC. It takes exactly one address, hence primaryAddress.
	args := append([]string{
		"-P", instanceName, serviceType, "local", strconv.Itoa(port), fqdn, address,
	}, txtRecords...)

	registration, err := startRegistration(log, args)
	if err != nil {
		return nil, err
	}

	log.Info("advertising over mDNS",
		"hostname", fqdn, "port", port, "address", address, "responder", "mDNSResponder")
	return registration, nil
}

// registration is the running dns-sd process. It holds the records for as long
// as it lives, so it is supervised rather than started and forgotten.
type registration struct {
	log  *slog.Logger
	cmd  *exec.Cmd
	stop sync.Once

	mu       sync.Mutex
	stopping bool
}

func startRegistration(log *slog.Logger, args []string) (*registration, error) {
	cmd := exec.Command(dnssd, args...)
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("read from %s: %w", dnssd, err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", dnssd, err)
	}

	reg := &registration{log: log, cmd: cmd}
	if err := reg.awaitConfirmation(output); err != nil {
		reg.Close()
		return nil, err
	}
	go reg.supervise()
	return reg, nil
}

// awaitConfirmation waits for mDNSResponder to say the name is live. Without
// this the name could be rejected as already in use and nothing would notice
// until somebody at a counter could not load the app.
func (r *registration) awaitConfirmation(output io.Reader) error {
	confirmed := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			line := scanner.Text()
			r.log.Debug("dns-sd", "line", line)

			switch {
			case strings.Contains(line, "Name now registered and active"):
				confirmed <- nil
				return
			case strings.Contains(line, "Name in use"):
				confirmed <- fmt.Errorf("another machine on this network already advertises that name")
				return
			}
		}
		confirmed <- fmt.Errorf("%s exited without registering the name", dnssd)
	}()

	select {
	case err := <-confirmed:
		return err
	case <-time.After(registrationTimeout):
		return fmt.Errorf("%s did not confirm the registration within %s", dnssd, registrationTimeout)
	}
}

// supervise reports the registration dying on its own. It does not restart it:
// the name is a convenience, the IP address still works, and a restart loop
// against a genuine name conflict would only fill the log.
func (r *registration) supervise() {
	err := r.cmd.Wait()

	r.mu.Lock()
	stopping := r.stopping
	r.mu.Unlock()
	if stopping {
		return
	}
	r.log.Warn("the mDNS registration stopped; the server is still reachable by IP address", "error", err)
}

func (r *registration) Close() error {
	r.mu.Lock()
	r.stopping = true
	r.mu.Unlock()

	var err error
	r.stop.Do(func() {
		if r.cmd.Process != nil {
			err = r.cmd.Process.Kill()
		}
	})
	return err
}
