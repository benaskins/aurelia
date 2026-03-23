package api

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/benaskins/aurelia/internal/daemon"
	"github.com/benaskins/aurelia/internal/node"
)

func setupTestServer(t *testing.T, specs map[string]string) (*Server, *http.Client) {
	t.Helper()

	dir := t.TempDir()
	for name, content := range specs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	d := daemon.NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	// Wait for processes to start
	time.Sleep(100 * time.Millisecond)

	srv := NewServer(d, nil)

	// Use a random Unix socket
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	go srv.ListenUnix(sockPath)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	// Wait for socket to be ready
	for i := 0; i < 20; i++ {
		conn, err := net.Dial("unix", sockPath)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", sockPath)
		},
	}
	t.Cleanup(func() { transport.CloseIdleConnections() })

	client := &http.Client{Transport: transport}

	return srv, client
}

func TestHealthEndpoint(t *testing.T) {
	_, client := setupTestServer(t, nil)

	resp, err := client.Get("http://aurelia/v1/health")
	if err != nil {
		t.Fatalf("GET /v1/health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "ok" {
		t.Errorf("expected status ok, got %q", result["status"])
	}
}

func TestListServices(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"svc.yaml": `
service:
  name: test-svc
  type: native
  command: "sleep 30"
`,
	})

	resp, err := client.Get("http://aurelia/v1/services")
	if err != nil {
		t.Fatalf("GET /v1/services: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var states []daemon.ServiceState
	json.NewDecoder(resp.Body).Decode(&states)
	if len(states) != 1 {
		t.Fatalf("expected 1 service, got %d", len(states))
	}
	if states[0].Name != "test-svc" {
		t.Errorf("expected 'test-svc', got %q", states[0].Name)
	}
}

func TestGetService(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"svc.yaml": `
service:
  name: my-svc
  type: native
  command: "sleep 30"
`,
	})

	// Existing service
	resp, err := client.Get("http://aurelia/v1/services/my-svc")
	if err != nil {
		t.Fatalf("GET /v1/services/my-svc: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var state daemon.ServiceState
	json.NewDecoder(resp.Body).Decode(&state)
	if state.Name != "my-svc" {
		t.Errorf("expected 'my-svc', got %q", state.Name)
	}

	// Non-existent service
	resp2, err := client.Get("http://aurelia/v1/services/nope")
	if err != nil {
		t.Fatalf("GET /v1/services/nope: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp2.StatusCode)
	}
}

func TestInspectService(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"svc.yaml": `
service:
  name: my-svc
  type: native
  command: "sleep 30"
network:
  port: 0
routing:
  hostname: my-svc.hestia.internal
  tls: true
env:
  BASE_CURRENCY: AUD
`,
	})

	resp, err := client.Get("http://aurelia/v1/services/my-svc/inspect")
	if err != nil {
		t.Fatalf("GET /v1/services/my-svc/inspect: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var si daemon.ServiceInspect
	json.NewDecoder(resp.Body).Decode(&si)
	if si.Name != "my-svc" {
		t.Errorf("name = %q, want my-svc", si.Name)
	}
	if si.Command != "sleep 30" {
		t.Errorf("command = %q, want sleep 30", si.Command)
	}
	if si.Env["BASE_CURRENCY"] != "AUD" {
		t.Errorf("env BASE_CURRENCY = %q, want AUD", si.Env["BASE_CURRENCY"])
	}
	if si.Routing == nil || si.Routing.Hostname != "my-svc.hestia.internal" {
		t.Errorf("routing = %v, want hostname my-svc.hestia.internal", si.Routing)
	}

	// Non-existent service
	resp2, err := client.Get("http://aurelia/v1/services/nope/inspect")
	if err != nil {
		t.Fatalf("GET /v1/services/nope/inspect: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp2.StatusCode)
	}
}

func TestStopStartService(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"svc.yaml": `
service:
  name: ctl-svc
  type: native
  command: "sleep 30"
`,
	})

	// Stop
	resp, err := client.Post("http://aurelia/v1/services/ctl-svc/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("POST stop: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 202 {
		t.Errorf("expected 202, got %d", resp.StatusCode)
	}

	// Start
	resp2, err := client.Post("http://aurelia/v1/services/ctl-svc/start", "application/json", nil)
	if err != nil {
		t.Fatalf("POST start: %v", err)
	}
	resp2.Body.Close()

	if resp2.StatusCode != 202 {
		t.Errorf("expected 202, got %d", resp2.StatusCode)
	}
}

func TestRestartService(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"svc.yaml": `
service:
  name: rst-svc
  type: native
  command: "sleep 30"
`,
	})

	resp, err := client.Post("http://aurelia/v1/services/rst-svc/restart", "application/json", nil)
	if err != nil {
		t.Fatalf("POST restart: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 202 {
		t.Errorf("expected 202, got %d", resp.StatusCode)
	}
}

func TestReload(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"svc.yaml": `
service:
  name: reload-svc
  type: native
  command: "sleep 30"
`,
	})

	resp, err := client.Post("http://aurelia/v1/reload", "application/json", nil)
	if err != nil {
		t.Fatalf("POST reload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestExternalServiceAPIGuard(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"ext.yaml": `
service:
  name: ext-svc
  type: external

health:
  type: tcp
  port: 19876
  interval: 1s
  timeout: 500ms
`,
	})

	// start should be rejected
	resp, err := client.Post("http://aurelia/v1/services/ext-svc/start", "application/json", nil)
	if err != nil {
		t.Fatalf("POST start: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for start external, got %d", resp.StatusCode)
	}

	// stop should be rejected
	resp, err = client.Post("http://aurelia/v1/services/ext-svc/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("POST stop: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for stop external, got %d", resp.StatusCode)
	}

	// restart should be rejected
	resp, err = client.Post("http://aurelia/v1/services/ext-svc/restart", "application/json", nil)
	if err != nil {
		t.Fatalf("POST restart: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for restart external, got %d", resp.StatusCode)
	}

	// deploy should be rejected
	resp, err = client.Post("http://aurelia/v1/services/ext-svc/deploy", "application/json", nil)
	if err != nil {
		t.Fatalf("POST deploy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for deploy external, got %d", resp.StatusCode)
	}

	// GET (status) should still work
	resp, err = client.Get("http://aurelia/v1/services/ext-svc")
	if err != nil {
		t.Fatalf("GET service: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 for GET external, got %d", resp.StatusCode)
	}
}

func TestTCPAuthRequired(t *testing.T) {
	d := daemon.NewDaemon(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	srv := NewServer(d, nil)
	tokenPath := filepath.Join(t.TempDir(), "api.token")
	if err := srv.GenerateToken(tokenPath); err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// Start TCP listener on a random port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close() // free the port for ListenTCP

	go srv.ListenTCP(addr)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	// Wait for TCP to be ready
	for i := 0; i < 20; i++ {
		if conn, err := net.Dial("tcp", addr); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	baseURL := fmt.Sprintf("http://%s", addr)

	// No token — should get 401
	resp, err := http.Get(baseURL + "/v1/health")
	if err != nil {
		t.Fatalf("GET without token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("expected 401 without token, got %d", resp.StatusCode)
	}

	// Wrong token — should get 401
	req, _ := http.NewRequest("GET", baseURL+"/v1/health", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with wrong token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("expected 401 with wrong token, got %d", resp.StatusCode)
	}

	// Correct token — should get 200
	token, _ := os.ReadFile(tokenPath)
	req, _ = http.NewRequest("GET", baseURL+"/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+string(token))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with correct token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 with correct token, got %d", resp.StatusCode)
	}
}

func TestTCPRequiresToken(t *testing.T) {
	srv := NewServer(daemon.NewDaemon(t.TempDir()), nil)
	err := srv.ListenTCP("127.0.0.1:0")
	if err == nil {
		t.Fatal("expected error when calling ListenTCP without GenerateToken")
	}
}

func TestServiceLogsCapN(t *testing.T) {
	_, client := setupTestServer(t, map[string]string{
		"svc.yaml": `
service:
  name: log-svc
  type: native
  command: "echo hello"
`,
	})

	// Wait for process to run and produce output
	time.Sleep(200 * time.Millisecond)

	// Request an absurdly large number of lines — should be capped, not OOM
	resp, err := client.Get("http://aurelia/v1/services/log-svc/logs?n=999999999")
	if err != nil {
		t.Fatalf("GET logs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// The response should succeed without hanging or OOM
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["lines"] == nil {
		t.Error("expected lines field in response")
	}
}

func TestListenTCPNonLoopbackWarning(t *testing.T) {
	d := daemon.NewDaemon(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	srv := NewServer(d, nil)
	tokenPath := filepath.Join(t.TempDir(), "api.token")
	if err := srv.GenerateToken(tokenPath); err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	go srv.ListenTCP("0.0.0.0:0")
	time.Sleep(100 * time.Millisecond)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	if !strings.Contains(buf.String(), "non-loopback") {
		t.Errorf("expected non-loopback warning in logs, got: %s", buf.String())
	}
}

func TestListenTCPLoopbackNoWarning(t *testing.T) {
	d := daemon.NewDaemon(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	srv := NewServer(d, nil)
	tokenPath := filepath.Join(t.TempDir(), "api.token")
	if err := srv.GenerateToken(tokenPath); err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	go srv.ListenTCP("127.0.0.1:0")
	time.Sleep(100 * time.Millisecond)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	if strings.Contains(buf.String(), "non-loopback") {
		t.Errorf("unexpected non-loopback warning for 127.0.0.1: %s", buf.String())
	}
}

func setupTestServerWithPeers(t *testing.T, specs map[string]string, peers []*node.Client) (*Server, *http.Client) {
	t.Helper()

	dir := t.TempDir()
	for name, content := range specs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	opts := []daemon.Option{}
	if len(peers) > 0 {
		opts = append(opts, daemon.WithPeers(peers))
	}
	d := daemon.NewDaemon(dir, opts...)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	time.Sleep(100 * time.Millisecond)

	srv := NewServer(d, nil)

	// Use /tmp for socket to avoid macOS 104-char Unix socket path limit
	sockDir, err := os.MkdirTemp("/tmp", "aurelia-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "t.sock")
	go srv.ListenUnix(sockPath)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	for i := 0; i < 20; i++ {
		conn, err := net.Dial("unix", sockPath)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", sockPath)
		},
	}
	t.Cleanup(func() { transport.CloseIdleConnections() })

	return srv, &http.Client{Transport: transport}
}

func TestClusterServicesAggregation(t *testing.T) {
	// Set up a fake peer daemon
	peerSrv := fakePeerServer(t, []daemon.ServiceState{
		{Name: "remote-svc", Type: "native", State: "running", Node: "limen"},
	})
	defer peerSrv.Close()

	peer := node.New("limen", peerSrv.Listener.Addr().String(), "tok")
	_, client := setupTestServerWithPeers(t, map[string]string{
		"svc.yaml": `
service:
  name: local-svc
  type: native
  command: "sleep 30"
`,
	}, []*node.Client{peer})

	resp, err := client.Get("http://aurelia/v1/cluster/services")
	if err != nil {
		t.Fatalf("GET /v1/cluster/services: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var clusterResp struct {
		Services []daemon.ServiceState `json:"services"`
		Peers    map[string]string     `json:"peers"`
	}
	json.NewDecoder(resp.Body).Decode(&clusterResp)

	// Should have both local and remote services
	names := make(map[string]bool)
	for _, s := range clusterResp.Services {
		names[s.Name] = true
	}
	if !names["local-svc"] {
		t.Error("expected local-svc in cluster services")
	}
	if !names["remote-svc"] {
		t.Error("expected remote-svc in cluster services")
	}

	// Local service should have node stamped
	for _, s := range clusterResp.Services {
		if s.Name == "local-svc" && s.Node == "" {
			t.Error("expected local-svc to have Node field set")
		}
	}

	// Peers should have status
	if clusterResp.Peers["limen"] != "ok" {
		t.Errorf("peer limen status = %q, want %q", clusterResp.Peers["limen"], "ok")
	}
}

func TestClusterServicesProxyCommand(t *testing.T) {
	var gotPath, gotMethod string
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer peerSrv.Close()

	peer := node.New("limen", peerSrv.Listener.Addr().String(), "tok")
	_, client := setupTestServerWithPeers(t, nil, []*node.Client{peer})

	// Proxy restart to remote node
	resp, err := client.Post("http://aurelia/v1/cluster/services/foo/restart?node=limen", "application/json", nil)
	if err != nil {
		t.Fatalf("POST cluster restart: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if gotPath != "/v1/services/foo/restart" {
		t.Errorf("proxied path = %q, want /v1/services/foo/restart", gotPath)
	}
	if gotMethod != "POST" {
		t.Errorf("proxied method = %q, want POST", gotMethod)
	}
}

func TestClusterServicesProxyUnknownNode(t *testing.T) {
	_, client := setupTestServerWithPeers(t, nil, nil)

	resp, err := client.Post("http://aurelia/v1/cluster/services/foo/restart?node=unknown", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("expected 404 for unknown node, got %d", resp.StatusCode)
	}
}

func fakePeerServer(t *testing.T, states []daemon.ServiceState) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/services":
			json.NewEncoder(w).Encode(states)
		case "/v1/health":
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}
	}))
}

// testCA generates a self-signed CA, server cert, and optional client cert for testing.
// Returns paths to the CA cert, server cert, server key, client cert, and client key.
type testCerts struct {
	CAPath         string
	ServerCertPath string
	ServerKeyPath  string
	ClientCertPath string
	ClientKeyPath  string
}

func generateTestCerts(t *testing.T, clientCN string) testCerts {
	t.Helper()
	dir := t.TempDir()

	// CA key and cert
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(1 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		t.Fatal(err)
	}

	writePEM := func(path string, typ string, data []byte) {
		t.Helper()
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		pem.Encode(f, &pem.Block{Type: typ, Bytes: data})
	}
	writeKey := func(path string, key *ecdsa.PrivateKey) {
		t.Helper()
		data, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		writePEM(path, "EC PRIVATE KEY", data)
	}

	caPath := filepath.Join(dir, "ca.crt")
	writePEM(caPath, "CERTIFICATE", caCertDER)

	// Server cert
	serverKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(1 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	serverCertDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	serverCertPath := filepath.Join(dir, "server.crt")
	serverKeyPath := filepath.Join(dir, "server.key")
	writePEM(serverCertPath, "CERTIFICATE", serverCertDER)
	writeKey(serverKeyPath, serverKey)

	tc := testCerts{
		CAPath:         caPath,
		ServerCertPath: serverCertPath,
		ServerKeyPath:  serverKeyPath,
	}

	// Client cert (for mTLS)
	if clientCN != "" {
		clientKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		clientTemplate := &x509.Certificate{
			SerialNumber: big.NewInt(3),
			Subject:      pkix.Name{CommonName: clientCN},
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(1 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		}
		clientCertDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, &clientKey.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		tc.ClientCertPath = filepath.Join(dir, "client.crt")
		tc.ClientKeyPath = filepath.Join(dir, "client.key")
		writePEM(tc.ClientCertPath, "CERTIFICATE", clientCertDER)
		writeKey(tc.ClientKeyPath, clientKey)
	}

	return tc
}

func TestTLSAuthMTLSClient(t *testing.T) {
	certs := generateTestCerts(t, "limen")

	d := daemon.NewDaemon(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	srv := NewServer(d, nil)
	tokenPath := filepath.Join(t.TempDir(), "api.token")
	if err := srv.GenerateToken(tokenPath); err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	serverTLS, err := LoadTLSConfig(certs.ServerCertPath, certs.ServerKeyPath, certs.CAPath)
	if err != nil {
		t.Fatalf("LoadTLSConfig: %v", err)
	}

	// Bind to a port, then serve
	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	addr := ln.Addr().String()

	srv.tcpServer = &http.Server{
		Handler: srv.requireAuth(srv.server.Handler),
	}
	go srv.tcpServer.Serve(ln)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	// Load CA for client
	caPEM, _ := os.ReadFile(certs.CAPath)
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	// Test 1: mTLS client with valid cert should succeed
	clientCert, err := tls.LoadX509KeyPair(certs.ClientCertPath, certs.ClientKeyPath)
	if err != nil {
		t.Fatalf("loading client cert: %v", err)
	}
	mtlsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{clientCert},
				RootCAs:      caPool,
			},
		},
	}
	resp, err := mtlsClient.Get("https://" + addr + "/v1/health")
	if err != nil {
		t.Fatalf("mTLS GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("mTLS: expected 200, got %d", resp.StatusCode)
	}

	// Test 2: No client cert, no token should fail
	noAuthClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: caPool,
			},
		},
	}
	resp, err = noAuthClient.Get("https://" + addr + "/v1/health")
	if err != nil {
		t.Fatalf("no-auth GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("no-auth: expected 401, got %d", resp.StatusCode)
	}

	// Test 3: No client cert but valid bearer token should succeed
	tokenBytes, _ := os.ReadFile(tokenPath)
	req, _ := http.NewRequest("GET", "https://"+addr+"/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tokenBytes)))
	resp, err = noAuthClient.Do(req)
	if err != nil {
		t.Fatalf("bearer GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("bearer: expected 200, got %d", resp.StatusCode)
	}
}

func TestTLSPeerIdentityFromCert(t *testing.T) {
	certs := generateTestCerts(t, "hestia")

	d := daemon.NewDaemon(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	// Custom handler that returns the peer identity
	srv := NewServer(d, nil)
	tokenPath := filepath.Join(t.TempDir(), "api.token")
	srv.GenerateToken(tokenPath)

	identityHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := PeerIdentity(r.Context())
		writeJSON(w, http.StatusOK, map[string]string{"peer": id})
	})

	serverTLS, _ := LoadTLSConfig(certs.ServerCertPath, certs.ServerKeyPath, certs.CAPath)
	ln, _ := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	addr := ln.Addr().String()

	srv.tcpServer = &http.Server{
		Handler: srv.requireAuth(identityHandler),
	}
	go srv.tcpServer.Serve(ln)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	caPEM, _ := os.ReadFile(certs.CAPath)
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	// mTLS client should get CN as identity
	clientCert, _ := tls.LoadX509KeyPair(certs.ClientCertPath, certs.ClientKeyPath)
	mtlsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{clientCert},
				RootCAs:      caPool,
			},
		},
	}
	resp, err := mtlsClient.Get("https://" + addr + "/v1/health")
	if err != nil {
		t.Fatalf("mTLS GET: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["peer"] != "hestia" {
		t.Errorf("peer identity = %q, want %q", result["peer"], "hestia")
	}

	// Bearer token client should get "cli" as identity
	tokenBytes, _ := os.ReadFile(tokenPath)
	req, _ := http.NewRequest("GET", "https://"+addr+"/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tokenBytes)))
	noAuthClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: caPool},
		},
	}
	resp2, err := noAuthClient.Do(req)
	if err != nil {
		t.Fatalf("bearer GET: %v", err)
	}
	defer resp2.Body.Close()

	var result2 map[string]string
	json.NewDecoder(resp2.Body).Decode(&result2)
	if result2["peer"] != "cli" {
		t.Errorf("peer identity = %q, want %q", result2["peer"], "cli")
	}
}

func TestAuditLogMiddleware(t *testing.T) {
	certs := generateTestCerts(t, "limen")

	d := daemon.NewDaemon(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	srv := NewServer(d, nil)
	tokenPath := filepath.Join(t.TempDir(), "api.token")
	srv.GenerateToken(tokenPath)

	// Capture log output
	var logBuf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&logBuf, nil))

	serverTLS, _ := LoadTLSConfig(certs.ServerCertPath, certs.ServerKeyPath, certs.CAPath)
	ln, _ := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	addr := ln.Addr().String()

	srv.tcpServer = &http.Server{
		Handler: srv.requireAuth(srv.auditLog(srv.server.Handler)),
	}
	go srv.tcpServer.Serve(ln)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	caPEM, _ := os.ReadFile(certs.CAPath)
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	// Make a request with mTLS
	clientCert, _ := tls.LoadX509KeyPair(certs.ClientCertPath, certs.ClientKeyPath)
	mtlsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{clientCert},
				RootCAs:      caPool,
			},
		},
	}
	resp, err := mtlsClient.Get("https://" + addr + "/v1/health")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	// Check audit log contains expected fields
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "api.request") {
		t.Errorf("expected audit log message, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"peer":"limen"`) {
		t.Errorf("expected peer identity 'limen' in audit log, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"method":"GET"`) {
		t.Errorf("expected method GET in audit log, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"path":"/v1/health"`) {
		t.Errorf("expected path /v1/health in audit log, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"status":200`) {
		t.Errorf("expected status 200 in audit log, got: %s", logOutput)
	}
}

func TestTokenRotationDualTokenWindow(t *testing.T) {
	d := daemon.NewDaemon(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	srv := NewServer(d, nil)
	tokenPath := filepath.Join(t.TempDir(), "api.token")
	srv.GenerateToken(tokenPath)
	oldToken := srv.token

	// Rotate
	newToken, err := srv.RotateToken()
	if err != nil {
		t.Fatalf("RotateToken: %v", err)
	}
	if newToken == oldToken {
		t.Error("new token should differ from old")
	}

	// Both tokens should be valid during dual-token window
	if !srv.validToken(oldToken) {
		t.Error("old token should be valid during rotation")
	}
	if !srv.validToken(newToken) {
		t.Error("new token should be valid during rotation")
	}

	// Commit rotation
	srv.CommitTokenRotation()

	// Only new token should be valid
	if srv.validToken(oldToken) {
		t.Error("old token should be invalid after commit")
	}
	if !srv.validToken(newToken) {
		t.Error("new token should still be valid after commit")
	}
}

func TestPeerTokenUpdateRequiresMTLS(t *testing.T) {
	certs := generateTestCerts(t, "limen")

	d := daemon.NewDaemon(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() { d.Stop(5 * time.Second) })

	srv := NewServer(d, nil)
	tokenPath := filepath.Join(t.TempDir(), "api.token")
	srv.GenerateToken(tokenPath)

	serverTLS, _ := LoadTLSConfig(certs.ServerCertPath, certs.ServerKeyPath, certs.CAPath)
	ln, _ := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	addr := ln.Addr().String()

	srv.tcpServer = &http.Server{
		Handler: srv.rateLimiter.handler(srv.requireAuth(srv.auditLog(srv.server.Handler))),
	}
	go srv.tcpServer.Serve(ln)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	caPEM, _ := os.ReadFile(certs.CAPath)
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	// Bearer token client should be forbidden from peer token update
	tokenBytes, _ := os.ReadFile(tokenPath)
	req, _ := http.NewRequest("POST", "https://"+addr+"/v1/peer/token", strings.NewReader(`{"node":"hestia","token":"new-tok"}`))
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tokenBytes)))
	req.Header.Set("Content-Type", "application/json")

	noAuthClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: caPool},
		},
	}
	resp, err := noAuthClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for CLI client, got %d", resp.StatusCode)
	}
}

func TestLoadTLSConfigInvalidPaths(t *testing.T) {
	t.Parallel()

	_, err := LoadTLSConfig("/nonexistent/cert", "/nonexistent/key", "/nonexistent/ca")
	if err == nil {
		t.Error("expected error for nonexistent cert paths")
	}
}

func TestListenTLSRequiresToken(t *testing.T) {
	srv := NewServer(daemon.NewDaemon(t.TempDir()), nil)
	tlsCfg := &tls.Config{}
	err := srv.ListenTLS("127.0.0.1:0", tlsCfg)
	if err == nil {
		t.Fatal("expected error when calling ListenTLS without GenerateToken")
	}
}
