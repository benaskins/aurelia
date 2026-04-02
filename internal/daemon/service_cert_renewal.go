package daemon

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/benaskins/aurelia/internal/node"
)

// ServiceCert describes a certificate that should be auto-renewed.
type ServiceCert struct {
	Role     string // PKI role (server, client)
	CN       string // common name
	TTL      string // requested TTL
	CertDir  string // directory containing the cert files
	IsClient bool   // true for client certs (client.crt), false for server (cert.crt)
}

// ServiceCertRenewal manages automatic renewal of service TLS certificates
// (server and client certs) via the CA peer node.
type ServiceCertRenewal struct {
	certs   []ServiceCert
	adyton  *node.Client
	mu      sync.Mutex
	logger  *slog.Logger

	// postRenew is called after any cert is renewed (e.g. to restart traefik).
	postRenew func()
}

// NewServiceCertRenewal creates a renewal manager for the given certs.
func NewServiceCertRenewal(certs []ServiceCert, adyton *node.Client, postRenew func()) *ServiceCertRenewal {
	return &ServiceCertRenewal{
		certs:     certs,
		adyton:    adyton,
		postRenew: postRenew,
		logger:    slog.With("component", "service-cert-renewal"),
	}
}

// CheckAndRenew checks all managed certs and renews any that are past 2/3 of their lifetime.
// Returns the number of certs renewed.
func (r *ServiceCertRenewal) CheckAndRenew() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	renewed := 0
	for _, sc := range r.certs {
		certFile := r.certPath(sc)
		if !r.needsRenewal(certFile) {
			continue
		}

		r.logger.Info("renewing service cert", "role", sc.Role, "cn", sc.CN)

		resp, err := r.adyton.IssueCert(sc.Role, sc.CN, sc.TTL)
		if err != nil {
			r.logger.Error("service cert renewal failed", "cn", sc.CN, "error", err)
			continue
		}

		if err := r.writeCert(sc, resp); err != nil {
			r.logger.Error("writing renewed cert failed", "cn", sc.CN, "error", err)
			continue
		}

		r.logger.Info("service cert renewed", "cn", sc.CN, "serial", resp.Serial)
		renewed++
	}

	if renewed > 0 && r.postRenew != nil {
		r.postRenew()
	}

	return renewed
}

func (r *ServiceCertRenewal) certPath(sc ServiceCert) string {
	if sc.IsClient {
		return filepath.Join(sc.CertDir, "client.crt")
	}
	return filepath.Join(sc.CertDir, "cert.crt")
}

func (r *ServiceCertRenewal) needsRenewal(certFile string) bool {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return true // missing cert = needs renewal
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return true
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true
	}

	lifetime := cert.NotAfter.Sub(cert.NotBefore)
	renewAt := cert.NotBefore.Add(lifetime * 2 / 3)
	return time.Now().After(renewAt)
}

func (r *ServiceCertRenewal) writeCert(sc ServiceCert, resp *node.RenewCertResponse) error {
	if err := os.MkdirAll(sc.CertDir, 0755); err != nil {
		return fmt.Errorf("creating cert dir: %w", err)
	}

	if sc.IsClient {
		if err := os.WriteFile(filepath.Join(sc.CertDir, "client.crt"), []byte(resp.Certificate), 0644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(sc.CertDir, "client.key"), []byte(resp.PrivateKey), 0600); err != nil {
			return err
		}
	} else {
		if err := os.WriteFile(filepath.Join(sc.CertDir, "cert.crt"), []byte(resp.Certificate), 0644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(sc.CertDir, "cert.key"), []byte(resp.PrivateKey), 0600); err != nil {
			return err
		}
		fullchain := resp.Certificate + "\n" + resp.CAChain
		if err := os.WriteFile(filepath.Join(sc.CertDir, "fullchain.crt"), []byte(fullchain), 0644); err != nil {
			return err
		}
	}

	if err := os.WriteFile(filepath.Join(sc.CertDir, "ca-chain.crt"), []byte(resp.CAChain), 0644); err != nil {
		return err
	}
	return nil
}
