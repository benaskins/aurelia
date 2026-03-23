//go:build integration

package multinode

import (
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
