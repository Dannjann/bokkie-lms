package discovery

import (
	"io"
	"log/slog"
	"sync"
)

// Publisher owns the LAN name for the lifetime of the process, so advertising
// can be withdrawn and restored without restarting the server.
//
// Advertise alone was enough while the name was a start-up decision. It is not
// one any more: turning LAN access off at the dashboard must also stop telling
// the network where to find a server that will now refuse it. A phone that
// resolves bokkie.local and then gets a refusal is a worse answer than a phone
// that cannot find the name at all — the first looks like a fault, the second
// looks like what it is.
type Publisher struct {
	log      *slog.Logger
	hostname string
	port     int

	// allowed is config.yaml's mdns.enabled: a permanent veto from whoever
	// installed the machine. The dashboard switch can move published within
	// that veto but never past it, which is why the two are separate fields
	// rather than one that either side could stamp on.
	allowed bool

	mu     sync.Mutex
	active io.Closer
}

// NewPublisher builds a publisher that has not advertised anything yet.
// Nothing is published until SetPublished says so, because at construction the
// port is not open and the name would point at a server that cannot answer.
func NewPublisher(log *slog.Logger, hostname string, port int, allowed bool) *Publisher {
	return &Publisher{log: log, hostname: hostname, port: port, allowed: allowed}
}

// SetPublished brings the LAN name up or takes it down, and is safe to call
// with the state it is already in.
//
// An error here is worth reporting but never worth failing over: the server is
// still perfectly reachable by IP address, and refusing to serve the station
// because a name could not be advertised would be a worse outcome than the
// inconvenience it prevents. Every caller logs and carries on.
func (p *Publisher) SetPublished(published bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.allowed {
		if published {
			p.log.Info("mDNS advertising is switched off in config.yaml; reach the server by IP address")
		}
		return nil
	}

	switch {
	case published && p.active == nil:
		closer, err := Advertise(p.log, p.hostname, p.port)
		if err != nil {
			return err
		}
		p.active = closer

	case !published && p.active != nil:
		err := p.close()
		p.log.Info("withdrew the LAN name; only the host machine can reach the app now",
			"hostname", p.hostname+".local")
		return err
	}
	return nil
}

// Close withdraws the records on shutdown. Leaving stale ones behind sends the
// other machines to an address nothing is listening on.
func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.close()
}

// close withdraws the current registration. The caller holds p.mu.
func (p *Publisher) close() error {
	if p.active == nil {
		return nil
	}
	err := p.active.Close()
	p.active = nil
	return err
}
