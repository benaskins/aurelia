//go:build integration

package multinode

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestClusterFormation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	c := NewCluster(t, 3)
	defer c.Timings.Report(t)

	nodes := c.Nodes()
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}

	// Every node should be healthy
	for name, node := range nodes {
		done := c.Timings.Time("health-check")
		status, err := node.HealthCheck()
		done()
		if err != nil {
			t.Errorf("%s: health check failed: %v", name, err)
			continue
		}
		if status != 200 {
			t.Errorf("%s: expected 200, got %d", name, status)
		}
	}
}

func TestClusterAggregation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	c := NewCluster(t, 3)
	defer c.Timings.Report(t)

	// Give peers time to discover each other via liveness checks
	time.Sleep(15 * time.Second)

	// Query cluster services from node-1
	node1 := c.GetNode("node-1")
	if node1 == nil {
		t.Fatal("node-1 not found")
	}

	done := c.Timings.Time("cluster-aggregation")
	services, peers, err := node1.ClusterServices()
	done()

	if err != nil {
		t.Fatalf("cluster services: %v", err)
	}

	// Each node runs one "test-svc", so we expect 3 services
	if len(services) < 3 {
		t.Errorf("expected at least 3 services, got %d", len(services))
	}

	// Check peer status
	t.Logf("peers: %v", peers)
	for peerName, status := range peers {
		if status != "ok" {
			t.Errorf("peer %s status = %q, want %q", peerName, status, "ok")
		}
	}
}

func TestScaleUp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	c := NewCluster(t, 2)
	defer c.Timings.Report(t)

	// Verify 2 nodes running
	if len(c.Nodes()) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(c.Nodes()))
	}

	// Add a third node
	done := c.Timings.Time("scale-up")
	node3 := c.AddNode(t)
	done()

	if len(c.Nodes()) != 3 {
		t.Fatalf("expected 3 nodes after scale-up, got %d", len(c.Nodes()))
	}

	// New node should be healthy
	status, err := node3.HealthCheck()
	if err != nil {
		t.Fatalf("node-3 health check: %v", err)
	}
	if status != 200 {
		t.Errorf("node-3: expected 200, got %d", status)
	}
}

func TestScaleDown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	c := NewCluster(t, 3)
	defer c.Timings.Report(t)

	// Remove node-3
	done := c.Timings.Time("scale-down")
	c.RemoveNode("node-3")
	done()

	if len(c.Nodes()) != 2 {
		t.Fatalf("expected 2 nodes after scale-down, got %d", len(c.Nodes()))
	}

	// Remaining nodes should still be healthy
	for name, node := range c.Nodes() {
		status, err := node.HealthCheck()
		if err != nil {
			t.Errorf("%s: health check failed: %v", name, err)
			continue
		}
		if status != 200 {
			t.Errorf("%s: expected 200, got %d", name, status)
		}
	}
}

// --- Fault tolerance ---

func TestAggregationDeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	c := NewCluster(t, 3)
	defer c.Timings.Report(t)

	// Wait for peers to connect
	time.Sleep(15 * time.Second)

	// Kill node-3 so it becomes unreachable
	c.KillNode("node-3")

	// Wait for liveness to detect the dead node
	time.Sleep(15 * time.Second)

	// Aggregation from node-1 should complete within the 10s deadline
	// and report node-3 as unreachable/timeout
	done := c.Timings.Time("aggregation-with-dead-peer")
	_, peers, err := c.GetNode("node-1").ClusterServices()
	elapsed := c.Timings.Samples("aggregation-with-dead-peer")
	done()

	if err != nil {
		t.Fatalf("cluster services: %v", err)
	}

	t.Logf("peers: %v", peers)

	// Should complete well under the 10s deadline
	if len(elapsed) > 0 && elapsed[0] > 10*time.Second {
		t.Errorf("aggregation took %v, exceeds 10s deadline", elapsed[0])
	}
}

func TestNodeCrashRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	c := NewCluster(t, 2)
	defer c.Timings.Report(t)

	// Verify both healthy
	for name, node := range c.Nodes() {
		if status, err := node.HealthCheck(); err != nil || status != 200 {
			t.Fatalf("%s: unhealthy before crash: err=%v status=%d", name, err, status)
		}
	}

	// Kill node-2
	c.KillNode("node-2")

	// Node-1 should still be healthy
	if status, err := c.GetNode("node-1").HealthCheck(); err != nil || status != 200 {
		t.Fatalf("node-1 unhealthy after peer crash: err=%v status=%d", err, status)
	}
}

func TestNetworkPartition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	c := NewCluster(t, 3)
	defer c.Timings.Report(t)

	// Wait for peers to connect
	time.Sleep(15 * time.Second)

	// Partition node-2
	c.DisconnectNode(t, "node-2")

	// Wait for liveness to detect partition
	time.Sleep(15 * time.Second)

	// Aggregation from node-1 should skip partitioned node
	done := c.Timings.Time("aggregation-partitioned")
	services, peers, err := c.GetNode("node-1").ClusterServices()
	done()

	if err != nil {
		t.Fatalf("cluster services: %v", err)
	}

	t.Logf("services: %d, peers: %v", len(services), peers)

	// node-2 should be unreachable
	if status, ok := peers["node-2"]; ok && status == "ok" {
		t.Error("expected node-2 to be unreachable after partition")
	}

	// node-3 should still be ok
	if peers["node-3"] != "ok" {
		t.Errorf("node-3 status = %q, want ok", peers["node-3"])
	}

	// Reconnect
	c.ReconnectNode(t, "node-2")

	// Wait for liveness to recover (need 2+ liveness cycles at 10s each)
	time.Sleep(25 * time.Second)

	// Aggregation should now include all peers
	_, peersAfter, err := c.GetNode("node-1").ClusterServices()
	if err != nil {
		t.Fatalf("cluster services after reconnect: %v", err)
	}

	t.Logf("peers after reconnect: %v", peersAfter)
	if peersAfter["node-2"] != "ok" {
		t.Errorf("node-2 status after reconnect = %q, want ok", peersAfter["node-2"])
	}
}

// --- Security ---

func TestMTLSRejectsUntrustedCert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	c := NewCluster(t, 1)
	defer c.Timings.Report(t)

	node1 := c.GetNode("node-1")

	// Create a rogue CA and issue a cert from it
	rogueCA := NewTestCA(t)
	rogueCerts := rogueCA.IssueNodeCert(t, "rogue-node")

	rogueCert, err := tls.LoadX509KeyPair(rogueCerts.CertPath, rogueCerts.KeyPath)
	if err != nil {
		t.Fatalf("loading rogue cert: %v", err)
	}

	// Client with rogue cert should be rejected (TLS handshake failure)
	rogueClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates:       []tls.Certificate{rogueCert},
				InsecureSkipVerify: true,
			},
		},
	}

	resp, err := rogueClient.Get("https://" + node1.Addr + "/v1/health")
	if err != nil {
		// TLS handshake failure is expected
		t.Logf("rogue cert correctly rejected: %v", err)
		return
	}
	resp.Body.Close()
	// If the server somehow accepted the connection, it should still reject at auth
	if resp.StatusCode == 200 {
		t.Error("rogue cert should not get 200 from health endpoint")
	}
}

func TestMTLSRejectsNoCert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	c := NewCluster(t, 1)
	defer c.Timings.Report(t)

	node1 := c.GetNode("node-1")

	// Client with no cert and no bearer token should get 401
	noCertClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	resp, err := noCertClient.Get("https://" + node1.Addr + "/v1/health")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("expected 401 without cert or token, got %d", resp.StatusCode)
	}
}

// --- Performance ---

func TestConcurrentRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	c := NewCluster(t, 3)
	defer c.Timings.Report(t)

	time.Sleep(15 * time.Second)

	node1 := c.GetNode("node-1")
	concurrency := 50

	var wg sync.WaitGroup
	errors := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := c.Timings.Time("concurrent-health")
			status, err := node1.HealthCheck()
			done()
			if err != nil {
				errors <- err
				return
			}
			if status != 200 {
				errors <- fmt.Errorf("got status %d", status)
			}
		}()
	}
	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent request failed: %v", err)
	}
}

func TestAggregationScaling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	for _, n := range []int{2, 3, 5, 8, 13, 21, 34, 55, 89} {
		t.Run(fmt.Sprintf("%d-nodes", n), func(t *testing.T) {
			c := NewCluster(t, n)
			defer c.Timings.Report(t)

			// Wait for peers
			time.Sleep(15 * time.Second)

			node1 := c.GetNode("node-1")

			// Run aggregation 5 times to get stable numbers
			for i := 0; i < 5; i++ {
				done := c.Timings.Time(fmt.Sprintf("aggregation-%d-nodes", n))
				services, _, err := node1.ClusterServices()
				done()
				if err != nil {
					t.Fatalf("cluster services: %v", err)
				}
				if len(services) < n {
					t.Errorf("expected at least %d services, got %d", n, len(services))
				}
			}
		})
	}
}
