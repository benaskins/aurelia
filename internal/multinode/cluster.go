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
	"os/exec"
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
	daemonPort = "9090/tcp"
	imageTag   = "aurelia-test-node:latest"
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
// All nodes are pre-configured with the full peer list so they discover each other immediately.
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

	// Pre-plan: generate names and certs for all nodes
	names := make([]string, n)
	certs := make([]NodeCerts, n)
	for i := 0; i < n; i++ {
		names[i] = fmt.Sprintf("node-%d", i+1)
		certs[i] = c.ca.IssueNodeCert(t, names[i])
	}

	// Generate configs with full peer lists, then start all containers
	for i := 0; i < n; i++ {
		configDir := c.writeNodeConfigWithPeers(t, names[i], certs[i], names)
		c.startNode(t, names[i], certs[i], configDir)
	}

	// Wait for all peers to discover each other
	c.waitForPeerDiscovery(t, 30*time.Second)

	return c
}

// AddNode creates and starts a new daemon container.
// The new node knows about all existing nodes, but existing nodes won't
// discover it until their next config reload or restart.
func (c *Cluster) AddNode(t *testing.T) *TestNode {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()

	name := fmt.Sprintf("node-%d", len(c.nodes)+1)
	certs := c.ca.IssueNodeCert(t, name)

	// New node gets all existing peers in its config
	allNames := []string{name}
	for n := range c.nodes {
		allNames = append(allNames, n)
	}
	configDir := c.writeNodeConfigWithPeers(t, name, certs, allNames)
	return c.startNode(t, name, certs, configDir)
}

// startNode creates and starts a container for the named node.
func (c *Cluster) startNode(t *testing.T, name string, certs NodeCerts, configDir string) *TestNode {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        imageTag,
		ExposedPorts: []string{daemonPort},
		Networks:     []string{c.net.Name},
		NetworkAliases: map[string][]string{
			c.net.Name: {name},
		},
		WaitingFor: wait.ForListeningPort(daemonPort).
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

// repoRoot finds the aurelia repo root by walking up to find go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod found)")
		}
		dir = parent
	}
}

func buildImage(t *testing.T, _ context.Context) {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command("docker", "build",
		"-f", filepath.Join(root, "internal/multinode/Dockerfile"),
		"-t", imageTag,
		root,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building test image: %v\n%s", err, out)
	}
}

// writeNodeConfigWithPeers writes config and spec files for a node, including
// all other names in allNames as peers.
func (c *Cluster) writeNodeConfigWithPeers(t *testing.T, name string, certs NodeCerts, allNames []string) string {
	t.Helper()
	dir := t.TempDir()

	type nodeEntry struct {
		Name  string `yaml:"name"`
		Addr  string `yaml:"addr"`
		Token string `yaml:"token"`
	}
	var peers []nodeEntry
	for _, peerName := range allNames {
		if peerName == name {
			continue
		}
		peers = append(peers, nodeEntry{
			Name:  peerName,
			Addr:  peerName + ":9090",
			Token: "placeholder", // peers use mTLS, token is for CLI only
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
				Certificates:       []tls.Certificate{cert},
				RootCAs:            caPool,
				InsecureSkipVerify: true, // host connects via mapped port; hostname won't match cert CN
			},
		},
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

