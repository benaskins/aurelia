//go:build integration

package daemon

import (
	"context"
	"testing"
	"time"
)

func TestDaemonContainerService(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "web.yaml", `
service:
  name: test-web
  type: container
  image: alpine:latest
  network_mode: bridge
env:
  HELLO: world
restart:
  policy: never
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for container to be running
	time.Sleep(500 * time.Millisecond)

	states := d.ServiceStates()
	if len(states) != 1 {
		t.Fatalf("expected 1 service, got %d", len(states))
	}
	if states[0].Name != "test-web" {
		t.Errorf("expected test-web, got %q", states[0].Name)
	}
	if states[0].Type != "container" {
		t.Errorf("expected container type, got %q", states[0].Type)
	}

	d.Stop(10 * time.Second)
}

func TestDaemonMixedServices(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, "native.yaml", `
service:
  name: native-svc
  type: native
  command: "sleep 30"
restart:
  policy: never
`)
	writeSpec(t, dir, "container.yaml", `
service:
  name: container-svc
  type: container
  image: alpine:latest
  network_mode: bridge
restart:
  policy: never
`)

	d := NewDaemon(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	states := d.ServiceStates()
	if len(states) != 2 {
		t.Fatalf("expected 2 services, got %d", len(states))
	}

	typeMap := make(map[string]string)
	for _, s := range states {
		typeMap[s.Name] = s.Type
	}

	if typeMap["native-svc"] != "native" {
		t.Errorf("expected native type for native-svc, got %q", typeMap["native-svc"])
	}
	if typeMap["container-svc"] != "container" {
		t.Errorf("expected container type for container-svc, got %q", typeMap["container-svc"])
	}

	d.Stop(10 * time.Second)
}
