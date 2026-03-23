package multinode

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"testing"
)

func TestNewTestCA(t *testing.T) {
	ca := NewTestCA(t)

	if ca.Cert == nil {
		t.Fatal("CA cert is nil")
	}
	if !ca.Cert.IsCA {
		t.Error("expected CA cert to be a CA")
	}
	if ca.Key == nil {
		t.Fatal("CA key is nil")
	}

	// CA cert file should exist
	data, err := os.ReadFile(ca.CACertPath())
	if err != nil {
		t.Fatalf("reading CA cert: %v", err)
	}
	if len(data) == 0 {
		t.Error("CA cert file is empty")
	}
}

func TestIssueNodeCert(t *testing.T) {
	ca := NewTestCA(t)
	certs := ca.IssueNodeCert(t, "node-1")

	// All files should exist
	for _, path := range []string{certs.CACertPath, certs.CertPath, certs.KeyPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %s to exist: %v", path, err)
		}
	}

	// Cert should load as a valid TLS keypair
	pair, err := tls.LoadX509KeyPair(certs.CertPath, certs.KeyPath)
	if err != nil {
		t.Fatalf("loading keypair: %v", err)
	}
	if len(pair.Certificate) == 0 {
		t.Fatal("keypair has no certificates")
	}

	// Cert should be verifiable against the CA
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("parsing leaf: %v", err)
	}
	if leaf.Subject.CommonName != "node-1" {
		t.Errorf("CN = %q, want %q", leaf.Subject.CommonName, "node-1")
	}

	caPool := x509.NewCertPool()
	caPool.AddCert(ca.Cert)
	_, err = leaf.Verify(x509.VerifyOptions{
		Roots:     caPool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		t.Errorf("cert verification failed: %v", err)
	}
}

func TestIssueMultipleNodeCerts(t *testing.T) {
	ca := NewTestCA(t)

	certs1 := ca.IssueNodeCert(t, "node-1")
	certs2 := ca.IssueNodeCert(t, "node-2")
	certs3 := ca.IssueNodeCert(t, "node-3")

	// Each should have unique cert paths
	if certs1.CertPath == certs2.CertPath {
		t.Error("node-1 and node-2 have same cert path")
	}
	if certs2.CertPath == certs3.CertPath {
		t.Error("node-2 and node-3 have same cert path")
	}

	// Each should have unique serial numbers
	pairs := make([]*x509.Certificate, 3)
	for i, c := range []NodeCerts{certs1, certs2, certs3} {
		pair, _ := tls.LoadX509KeyPair(c.CertPath, c.KeyPath)
		leaf, _ := x509.ParseCertificate(pair.Certificate[0])
		pairs[i] = leaf
	}
	if pairs[0].SerialNumber.Cmp(pairs[1].SerialNumber) == 0 {
		t.Error("node-1 and node-2 have same serial")
	}
}

func TestCertFromDifferentCAFails(t *testing.T) {
	ca1 := NewTestCA(t)
	ca2 := NewTestCA(t)

	certs := ca1.IssueNodeCert(t, "rogue-node")
	pair, _ := tls.LoadX509KeyPair(certs.CertPath, certs.KeyPath)
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])

	// Verify against the wrong CA should fail
	wrongPool := x509.NewCertPool()
	wrongPool.AddCert(ca2.Cert)

	_, err := leaf.Verify(x509.VerifyOptions{
		Roots:     wrongPool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err == nil {
		t.Error("expected verification to fail against wrong CA")
	}
}
