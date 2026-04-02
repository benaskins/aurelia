package keychain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// IssuedCert holds the PEM-encoded certificate, private key, and CA chain
// returned by the PKI secrets engine.
type IssuedCert struct {
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
	CAChain     string `json:"ca_chain"`
	Serial      string `json:"serial_number"`
	Expiration  int64  `json:"expiration"`
}

// BaoPKIIssuer issues certificates from an OpenBao PKI secrets engine.
type BaoPKIIssuer struct {
	store      *BaoStore
	mount      string       // PKI mount path, e.g. "pki_lamina"
	preRequest func() error // called before each request to refresh tokens
}

// NewBaoPKIIssuer creates an issuer backed by the given BaoStore.
func NewBaoPKIIssuer(store *BaoStore, mount string) *BaoPKIIssuer {
	return &BaoPKIIssuer{store: store, mount: mount}
}

// Issue issues a certificate using the given role.
func (p *BaoPKIIssuer) Issue(role, commonName, ttl string) (*IssuedCert, error) {
	if p.preRequest != nil {
		if err := p.preRequest(); err != nil {
			return nil, fmt.Errorf("pki pre-request: %w", err)
		}
	}

	body := fmt.Sprintf(`{"common_name":%q,"ttl":%q}`, commonName, ttl)

	resp, err := p.store.do("PUT", fmt.Sprintf("/v1/%s/issue/%s", p.mount, role), strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("pki issue %s: %w", role, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("pki issue %s: status %d", role, resp.StatusCode)
	}

	var result struct {
		Data struct {
			Certificate  string   `json:"certificate"`
			PrivateKey   string   `json:"private_key"`
			CAChain      []string `json:"ca_chain"`
			SerialNumber string   `json:"serial_number"`
			Expiration   int64    `json:"expiration"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("pki issue %s: decode: %w", role, err)
	}

	return &IssuedCert{
		Certificate: result.Data.Certificate,
		PrivateKey:  result.Data.PrivateKey,
		CAChain:     strings.Join(result.Data.CAChain, "\n"),
		Serial:      result.Data.SerialNumber,
		Expiration:  result.Data.Expiration,
	}, nil
}

// IssueNodeCert issues a node certificate for mTLS daemon-to-daemon communication.
func (p *BaoPKIIssuer) IssueNodeCert(commonName, ttl string) (*IssuedCert, error) {
	return p.Issue("node", commonName, ttl)
}
