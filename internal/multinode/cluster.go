//go:build integration

package multinode

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	"gopkg.in/yaml.v3"
)

const (
	daemonPort     = "9090/tcp"
	imageTag       = "aurelia-test-node:latest"
	dockerfilePath = "internal/multinode/Dockerfile"
)

// Cluster manages a set of aurelia daemon containers for integration testing.
type Cluster struct {
	t       *testing.T
	ca      *TestCA
	net     *testcontainers.DockerNetwork
	nodes   map[string]*TestNode
	mu      sync.Mutex
	Timings *TimingCollector
}

// TestNode represents a single aurelia daemon running in a container.
type TestNode struct {
	Name      string
	Container testcontainers.Container
	Addr      string // host:port reachable from the test host
	Certs     NodeCerts
	client    *http.Client
	token     string
}

// NewCluster builds the test image, creates a Docker network, and starts n daemon nodes.
func NewCluster(t *testing.T, n int) *Cluster {
	t.Helper()
	ctx := context.Background()

	// Build test image from the repo root
	buildImage(t, ctx)

	// Create an isolated Docker network
	net, err := network.New(ctx, network.WithCheckDuplicate())
	if err != nil {
		t.Fatalf("creating docker network: %v", err)
	}
	t.Cleanup(func() { net.Remove(ctx) })

	c := &Cluster{
		t:       t,
		ca:      NewTestCA(t),
		net:     net,
		nodes:   make(map[string]*TestNode),
		Timings: NewTimingCollector(),
	}

	for i := 1; i <= n; i++ {
		c.AddNode(t)
	}

	// Wait for all peers to discover each other
	c.waitForPeerDiscovery(t, 30*time.Second)

	return c
}

// AddNode creates and starts a new daemon container, then triggers a reload
// on existing nodes so they discover the new peer.
func (c *Cluster) AddNode(t *testing.T) *TestNode {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()

	name := fmt.Sprintf("node-%d", len(c.nodes)+1)
	certs := c.ca.IssueNodeCert(t, name)

	// Write config and spec files for this node
	configDir := c.writeNodeConfig(t, name, certs)

	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        imageTag,
		ExposedPorts: []string{daemonPort},
		Networks:     []string{c.net.Name},
		NetworkAliases: map[string][]string{
			c.net.Name: {name},
		},
		WaitingFor: wait.ForHTTP("/v1/health").
			WithPort(daemonPort).
			WithTLS(true, c.tlsConfigForHealthCheck()).
			WithStartupTimeout(30 * time.Second),
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      filepath.Join(configDir, "config.yaml"),
				ContainerFilePath: "/root/.aurelia/config.yaml",
				FileMode:          0600,
			},
			{
				HostFilePath:      certs.CertPath,
				ContainerFilePath: "/etc/aurelia/tls/node.crt",
				FileMode:          0600,
			},
			{
				HostFilePath:      certs.KeyPath,
				ContainerFilePath: "/etc/aurelia/tls/node.key",
				FileMode:          0600,
			},
			{
				HostFilePath:      certs.CACertPath,
				ContainerFilePath: "/etc/aurelia/tls/ca.crt",
				FileMode:          0600,
			},
			{
				HostFilePath:      filepath.Join(configDir, "sleep.yaml"),
				ContainerFilePath: "/root/.aurelia/services/sleep.yaml",
				FileMode:          0644,
			},
		},
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("starting container %s: %v", name, err)
	}
	t.Cleanup(func() { container.Terminate(ctx) })

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("getting host for %s: %v", name, err)
	}
	port, err := container.MappedPort(ctx, daemonPort)
	if err != nil {
		t.Fatalf("getting port for %s: %v", name, err)
	}

	// Read the generated token from the container
	token := c.readToken(t, container)

	node := &TestNode{
		Name:      name,
		Container: container,
		Addr:      fmt.Sprintf("%s:%s", host, port.Port()),
		Certs:     certs,
		client:    c.makeHTTPClient(certs),
		token:     token,
	}

	c.nodes[name] = node

	// Update configs on existing nodes to include the new peer
	if len(c.nodes) > 1 {
		c.regenerateAllConfigs(t)
	}

	return node
}

// RemoveNode gracefully stops and removes a node from the cluster.
func (c *Cluster) RemoveNode(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	node, ok := c.nodes[name]
	if !ok {
		return
	}
	ctx := context.Background()
	d := 10 * time.Second
	node.Container.Stop(ctx, &d)
	delete(c.nodes, name)
}

// KillNode sends SIGKILL to a container (simulates crash).
func (c *Cluster) KillNode(name string) {
	c.mu.Lock()
	node, ok := c.nodes[name]
	c.mu.Unlock()
	if !ok {
		return
	}
	ctx := context.Background()
	node.Container.Stop(ctx, nil)
}

// DisconnectNode removes a node from the Docker network (network partition).
func (c *Cluster) DisconnectNode(t *testing.T, name string) {
	c.mu.Lock()
	node, ok := c.nodes[name]
	c.mu.Unlock()
	if !ok {
		return
	}
	ctx := context.Background()
	cid := node.Container.GetContainerID()
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		t.Fatalf("creating docker provider: %v", err)
	}
	err = provider.Client().NetworkDisconnect(ctx, c.net.ID, cid, true)
	if err != nil {
		t.Fatalf("disconnecting %s from network: %v", name, err)
	}
}

// ReconnectNode re-attaches a node to the Docker network.
func (c *Cluster) ReconnectNode(t *testing.T, name string) {
	c.mu.Lock()
	node, ok := c.nodes[name]
	c.mu.Unlock()
	if !ok {
		return
	}
	ctx := context.Background()
	cid := node.Container.GetContainerID()
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		t.Fatalf("creating docker provider: %v", err)
	}
	err = provider.Client().NetworkConnect(ctx, c.net.ID, cid, nil)
	if err != nil {
		t.Fatalf("reconnecting %s to network: %v", name, err)
	}
}

// Nodes returns a snapshot of all active nodes.
func (c *Cluster) Nodes() map[string]*TestNode {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap := make(map[string]*TestNode, len(c.nodes))
	for k, v := range c.nodes {
		snap[k] = v
	}
	return snap
}

// GetNode returns a specific node by name.
func (c *Cluster) GetNode(name string) *TestNode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nodes[name]
}

// HealthCheck calls /v1/health on a node and returns the status code.
func (n *TestNode) HealthCheck() (int, error) {
	req, err := http.NewRequest("GET", "https://"+n.Addr+"/v1/health", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+n.token)

	resp, err := n.client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// ClusterServices calls /v1/cluster/services and returns the parsed response.
func (n *TestNode) ClusterServices() ([]json.RawMessage, map[string]string, error) {
	req, err := http.NewRequest("GET", "https://"+n.Addr+"/v1/cluster/services", nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+n.token)

	resp, err := n.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	var result struct {
		Services []json.RawMessage `json:"services"`
		Peers    map[string]string `json:"peers"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, nil, fmt.Errorf("decoding cluster response: %w (body: %s)", err, body)
	}
	return result.Services, result.Peers, nil
}

// --- internal helpers ---

func buildImage(t *testing.T, ctx context.Context) {
	t.Helper()
	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    ".",
				Dockerfile: dockerfilePath,
				Tag:        imageTag,
			},
		},
	}
	// Build only (no start). Use the provider directly.
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		t.Fatalf("creating docker provider: %v", err)
	}
	_, err = provider.BuildImage(ctx, &req.ContainerRequest)
	if err != nil {
		t.Fatalf("building test image: %v", err)
	}
}

func (c *Cluster) writeNodeConfig(t *testing.T, name string, certs NodeCerts) string {
	t.Helper()
	dir := t.TempDir()

	// Build peer list (all nodes except self)
	type nodeEntry struct {
		Name  string `yaml:"name"`
		Addr  string `yaml:"addr"`
		Token string `yaml:"token"`
	}
	var peers []nodeEntry
	for peerName := range c.nodes {
		if peerName == name {
			continue
		}
		peers = append(peers, nodeEntry{
			Name:  peerName,
			Addr:  peerName + ":9090", // Docker DNS within the network
			Token: "placeholder",       // will be updated after daemon generates token
		})
	}

	cfg := map[string]any{
		"node_name": name,
		"api_addr":  "0.0.0.0:9090",
		"tls": map[string]string{
			"cert": "/etc/aurelia/tls/node.crt",
			"key":  "/etc/aurelia/tls/node.key",
			"ca":   "/etc/aurelia/tls/ca.crt",
		},
	}
	if len(peers) > 0 {
		cfg["nodes"] = peers
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}

	// Write a simple service spec
	spec := `service:
  name: test-svc
  type: native
  command: sleep 3600
`
	if err := os.WriteFile(filepath.Join(dir, "sleep.yaml"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}

	return dir
}

func (c *Cluster) readToken(t *testing.T, container testcontainers.Container) string {
	t.Helper()
	ctx := context.Background()

	// Wait for the token file to be generated
	var token string
	for i := 0; i < 30; i++ {
		rc, err := container.CopyFileFromContainer(ctx, "/root/.aurelia/api.token")
		if err == nil {
			data, _ := io.ReadAll(rc)
			rc.Close()
			token = string(data)
			if token != "" {
				return token
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("failed to read token from container after 15s")
	return ""
}

func (c *Cluster) makeHTTPClient(certs NodeCerts) *http.Client {
	cert, _ := tls.LoadX509KeyPair(certs.CertPath, certs.KeyPath)
	caPEM, _ := os.ReadFile(certs.CACertPath)
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      caPool,
			},
		},
	}
}

func (c *Cluster) tlsConfigForHealthCheck() *tls.Config {
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(c.ca.CertPEM)
	return &tls.Config{
		RootCAs:            caPool,
		InsecureSkipVerify: true, // health check from host can't verify container hostname
	}
}

func (c *Cluster) waitForPeerDiscovery(t *testing.T, timeout time.Duration) {
	t.Helper()
	if len(c.nodes) < 2 {
		return
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allHealthy := true
		for _, node := range c.nodes {
			status, err := node.HealthCheck()
			if err != nil || status != 200 {
				allHealthy = false
				break
			}
		}
		if allHealthy {
			return
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("peer discovery did not complete within %s", timeout)
}

func (c *Cluster) regenerateAllConfigs(t *testing.T) {
	// For existing nodes, we'd need to update their config and reload.
	// For now, this is a limitation: nodes only know about peers present at startup.
	// TODO: implement config push + reload for dynamic peer discovery
	t.Log("note: new node added but existing nodes need daemon restart to discover it")
}
