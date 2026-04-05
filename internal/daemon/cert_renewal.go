package daemon

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/benaskins/aurelia/internal/keychain"
	"github.com/benaskins/aurelia/internal/node"
)

// CertRenewal manages automatic mTLS certificate renewal.
// For peer nodes, it calls the PKI renew endpoint on adyton.
// For adyton itself, it issues directly via the PKI secrets engine.
type CertRenewal struct {
	certFile string // path to node cert PEM
	keyFile  string // path to node key PEM
	caFile   string // path to CA chain PEM
	nodeName string // local node name (used as CN)
	renewAt  time.Time
	mu       sync.Mutex
	logger   *slog.Logger

	// One of these is set depending on whether this is adyton or a peer.
	pkiIssuer *keychain.BaoPKIIssuer // non-nil on adyton (self-renewal)
	adyton    *node.Client           // non-nil on peers (call adyton to renew)
}

// CertRenewalConfig holds the configuration for cert renewal.
type CertRenewalConfig struct {
	CertFile  string
	KeyFile   string
	CAFile    string
	NodeName  string
	PKIIssuer *keychain.BaoPKIIssuer // set on adyton
	Adyton    *node.Client           // set on peers
}

// NewCertRenewal creates a CertRenewal from config.
// Returns an error if the current cert cannot be parsed.
func NewCertRenewal(cfg CertRenewalConfig) (*CertRenewal, error) {
	cr := &CertRenewal{
		certFile:  cfg.CertFile,
		keyFile:   cfg.KeyFile,
		caFile:    cfg.CAFile,
		nodeName:  cfg.NodeName,
		pkiIssuer: cfg.PKIIssuer,
		adyton:    cfg.Adyton,
		logger:    slog.With("component", "cert-renewal"),
	}

	renewAt, err := cr.computeRenewAt()
	if err != nil {
		return nil, fmt.Errorf("computing renewal time: %w", err)
	}
	cr.renewAt = renewAt
	cr.logger.Info("cert renewal scheduled",
		"node", cfg.NodeName,
		"renew_at", renewAt.Format(time.RFC3339),
		"mode", cr.mode(),
	)
	return cr, nil
}

// mode returns "local" (adyton self-renewal) or "peer" (call adyton).
func (cr *CertRenewal) mode() string {
	if cr.pkiIssuer != nil {
		return "local"
	}
	return "peer"
}

// CheckAndRenew checks if it's time to renew and does so if needed.
// Returns true if renewal was attempted.
func (cr *CertRenewal) CheckAndRenew() bool {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	if time.Now().Before(cr.renewAt) {
		return false
	}

	cr.logger.Info("cert renewal due", "node", cr.nodeName, "mode", cr.mode())

	var err error
	if cr.pkiIssuer != nil {
		err = cr.renewLocal()
	} else if cr.adyton != nil {
		err = cr.renewFromPeer()
	} else {
		cr.logger.Error("cert renewal: no issuer or adyton client configured")
		return true
	}

	if err != nil {
		cr.logger.Error("cert renewal failed", "node", cr.nodeName, "error", err)
		return true
	}

	// Recompute renewAt from the new cert
	renewAt, err := cr.computeRenewAt()
	if err != nil {
		cr.logger.Error("cert renewal: could not parse new cert", "error", err)
		return true
	}
	cr.renewAt = renewAt
	cr.logger.Info("cert renewed successfully",
		"node", cr.nodeName,
		"next_renewal", renewAt.Format(time.RFC3339),
	)
	return true
}

// RenewAt returns the time at which renewal is due.
func (cr *CertRenewal) RenewAt() time.Time {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.renewAt
}

// renewLocal issues a cert directly from OpenBao (adyton self-renewal).
func (cr *CertRenewal) renewLocal() error {
	cert, err := cr.pkiIssuer.IssueNodeCert(cr.nodeName, "72h")
	if err != nil {
		return fmt.Errorf("issuing cert: %w", err)
	}
	return cr.writeCert(cert.Certificate, cert.PrivateKey, cert.CAChain)
}

// renewFromPeer calls the adyton PKI renew endpoint.
func (cr *CertRenewal) renewFromPeer() error {
	resp, err := cr.adyton.RenewCert()
	if err != nil {
		return fmt.Errorf("renewing from adyton: %w", err)
	}
	return cr.writeCert(resp.Certificate, resp.PrivateKey, resp.CAChain)
}

// writeCert writes the cert, key, and CA chain to disk atomically.
func (cr *CertRenewal) writeCert(cert, key, caChain string) error {
	if err := os.WriteFile(cr.certFile, []byte(cert+"\n"), 0644); err != nil {
		return fmt.Errorf("writing cert: %w", err)
	}
	if err := os.WriteFile(cr.keyFile, []byte(key+"\n"), 0600); err != nil {
		return fmt.Errorf("writing key: %w", err)
	}
	if err := os.WriteFile(cr.caFile, []byte(caChain+"\n"), 0644); err != nil {
		return fmt.Errorf("writing ca chain: %w", err)
	}
	return nil
}

// computeRenewAt reads the current cert and returns the time at 2/3 of its lifetime.
func (cr *CertRenewal) computeRenewAt() (time.Time, error) {
	data, err := os.ReadFile(cr.certFile)
	if err != nil {
		return time.Time{}, fmt.Errorf("reading cert: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return time.Time{}, fmt.Errorf("no PEM block found in %s", cr.certFile)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing cert: %w", err)
	}

	lifetime := cert.NotAfter.Sub(cert.NotBefore)
	renewAt := cert.NotBefore.Add(lifetime * 2 / 3)
	return renewAt, nil
}
