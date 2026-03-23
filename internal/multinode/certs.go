package multinode

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCA holds a test certificate authority and can issue node certs on demand.
type TestCA struct {
	Cert    *x509.Certificate
	Key     *ecdsa.PrivateKey
	CertPEM []byte
	dir     string
	serial  int64
}

// NodeCerts holds the file paths for a node's TLS certificates.
type NodeCerts struct {
	CACertPath string
	CertPath   string
	KeyPath    string
}

// NewTestCA creates an ephemeral CA for a test run.
func NewTestCA(t *testing.T) *TestCA {
	t.Helper()
	dir := t.TempDir()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "aurelia-test-ca"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(1 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatal(err)
	}

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, caPEM, 0600); err != nil {
		t.Fatal(err)
	}

	return &TestCA{
		Cert:    cert,
		Key:     key,
		CertPEM: caPEM,
		dir:     dir,
		serial:  1,
	}
}

// CACertPath returns the path to the CA certificate PEM file.
func (ca *TestCA) CACertPath() string {
	return filepath.Join(ca.dir, "ca.crt")
}

// IssueNodeCert generates a server+client cert for a node.
// The cert includes both ServerAuth and ClientAuth extended key usage
// so it works for both serving TLS and mTLS client authentication.
func (ca *TestCA) IssueNodeCert(t *testing.T, nodeName string, ips ...net.IP) NodeCerts {
	t.Helper()
	ca.serial++

	nodeKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(ips) == 0 {
		ips = []net.IP{net.IPv4(127, 0, 0, 1)}
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(ca.serial),
		Subject:      pkix.Name{CommonName: nodeName},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(1 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  ips,
		DNSNames:     []string{nodeName},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, ca.Cert, &nodeKey.PublicKey, ca.Key)
	if err != nil {
		t.Fatal(err)
	}

	nodeDir := filepath.Join(ca.dir, nodeName)
	if err := os.MkdirAll(nodeDir, 0700); err != nil {
		t.Fatal(err)
	}

	certPath := filepath.Join(nodeDir, "node.crt")
	keyPath := filepath.Join(nodeDir, "node.key")

	writePEM(t, certPath, "CERTIFICATE", certDER)

	keyDER, err := x509.MarshalECPrivateKey(nodeKey)
	if err != nil {
		t.Fatal(err)
	}
	writePEM(t, keyPath, "EC PRIVATE KEY", keyDER)

	// Copy CA cert into node dir for easy mounting
	caPath := filepath.Join(nodeDir, "ca.crt")
	if err := os.WriteFile(caPath, ca.CertPEM, 0600); err != nil {
		t.Fatal(err)
	}

	return NodeCerts{
		CACertPath: caPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
	}
}

func writePEM(t *testing.T, path, typ string, data []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: typ, Bytes: data}); err != nil {
		t.Fatal(err)
	}
}
