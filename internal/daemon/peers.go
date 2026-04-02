package daemon

import (
	"context"
	"crypto/tls"
	"log/slog"
	"time"

	"github.com/benaskins/aurelia/internal/config"
	"github.com/benaskins/aurelia/internal/node"
)

const (
	// defaultLivenessInterval is how often peer health is checked.
	defaultLivenessInterval = 10 * time.Second
)

// WithPeers sets the peer node clients for the daemon.
func WithPeers(peers []*node.Client) Option {
	return func(d *Daemon) {
		d.peers = make(map[string]*node.Client, len(peers))
		for _, p := range peers {
			d.peers[p.Name] = p
		}
	}
}

// WithCertRenewal enables automatic mTLS certificate renewal.
func WithCertRenewal(cr *CertRenewal) Option {
	return func(d *Daemon) {
		d.certRenewal = cr
	}
}

// WithServiceCertRenewal enables automatic service certificate renewal.
func WithServiceCertRenewal(scr *ServiceCertRenewal) Option {
	return func(d *Daemon) {
		d.serviceCertRenewal = scr
	}
}

// BuildPeers creates node.Client instances from config, excluding the local node.
// If tlsConfig is non-nil, peers connect over TLS with the provided client cert.
func BuildPeers(cfg *config.Config, tlsConfig *tls.Config) []*node.Client {
	var peers []*node.Client
	for _, n := range cfg.Nodes {
		if n.Name == cfg.NodeName {
			continue // skip self
		}
		token, err := n.LoadToken()
		if err != nil {
			slog.Warn("skipping peer node, no token", "node", n.Name, "error", err)
			continue
		}
		if tlsConfig != nil {
			peers = append(peers, node.NewTLS(n.Name, n.Addr, token, tlsConfig))
		} else {
			peers = append(peers, node.New(n.Name, n.Addr, token))
		}
	}
	return peers
}

// PeerStates returns the current reachability of all peers.
func (d *Daemon) PeerStates() map[string]bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	states := make(map[string]bool, len(d.peerStatus))
	for name, ok := range d.peerStatus {
		states[name] = ok
	}
	return states
}

// Peers returns the peer clients.
func (d *Daemon) Peers() map[string]*node.Client {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.peers
}

// startPeerLiveness launches a goroutine that periodically pings all peers
// and checks certificate renewal.
func (d *Daemon) startPeerLiveness(ctx context.Context) {
	if len(d.peers) == 0 && d.certRenewal == nil && d.serviceCertRenewal == nil {
		return
	}
	go func() {
		if len(d.peers) > 0 {
			d.checkPeerLiveness()
		}
		ticker := time.NewTicker(defaultLivenessInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if len(d.peers) > 0 {
					d.checkPeerLiveness()
				}
				if d.certRenewal != nil {
					d.certRenewal.CheckAndRenew()
				}
				if d.serviceCertRenewal != nil {
					d.serviceCertRenewal.CheckAndRenew()
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// checkPeerLiveness pings each peer and updates reachability status.
func (d *Daemon) checkPeerLiveness() {
	for name, c := range d.peers {
		err := c.Health()
		d.mu.Lock()
		d.peerStatus[name] = err == nil
		d.mu.Unlock()
		if err != nil {
			// Close idle connections so the next check gets fresh DNS
			// resolution and TCP connections. Without this, the HTTP
			// client's connection pool holds stale connections after
			// a network partition, preventing recovery.
			c.CloseIdleConnections()
			d.logger.Warn("peer unreachable", "peer", name, "error", err)
		} else {
			d.logger.Debug("peer reachable", "peer", name)
		}
	}
}
