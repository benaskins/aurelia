//go:build integration

package examples_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/benaskins/aurelia/internal/daemon"
)

func TestExamples(t *testing.T) {
	dir := t.TempDir()

	writeSpec(t, dir, "hello-static.yaml", `
service:
  name: hello-static
  type: native
  command: /usr/local/bin/hello-static

network:
  port: 8080

health:
  type: http
  path: /health
  interval: 2s
  timeout: 2s
  grace_period: 3s

restart:
  policy: on-failure
  max_attempts: 3
  delay: 2s
`)

	writeSpec(t, dir, "hello-dynamic.yaml", `
service:
  name: hello-dynamic
  type: native
  command: /usr/local/bin/hello-dynamic

network:
  port: 0

health:
  type: http
  path: /health
  interval: 2s
  timeout: 2s
  grace_period: 3s

restart:
  policy: on-failure
  max_attempts: 3
  delay: 2s
`)

	d := daemon.NewDaemon(dir, daemon.WithPortRange(30000, 31000))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	defer d.Stop(10 * time.Second)

	// Wait for both services to reach running state.
	waitForServices(t, d, []string{"hello-static", "hello-dynamic"}, 30*time.Second)

	t.Run("static_port", func(t *testing.T) {
		body := httpGet(t, "http://127.0.0.1:8080/")
		if body != "Hello from static-port service!\n" {
			t.Errorf("unexpected response body: %q", body)
		}

		resp, err := http.Get("http://127.0.0.1:8080/health")
		if err != nil {
			t.Fatalf("health check: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("health status: got %d, want 200", resp.StatusCode)
		}
	})

	t.Run("dynamic_port", func(t *testing.T) {
		state, err := d.ServiceState("hello-dynamic")
		if err != nil {
			t.Fatalf("ServiceState: %v", err)
		}
		if state.Port == 0 {
			t.Fatal("expected a dynamically allocated port, got 0")
		}
		if state.Port < 30000 || state.Port > 31000 {
			t.Errorf("port %d outside expected range 30000-31000", state.Port)
		}

		body := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/", state.Port))
		expected := fmt.Sprintf("Hello from dynamic-port service! (port %d)\n", state.Port)
		if body != expected {
			t.Errorf("unexpected response body: %q, want %q", body, expected)
		}

		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", state.Port))
		if err != nil {
			t.Fatalf("health check: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("health status: got %d, want 200", resp.StatusCode)
		}
	})
}

// waitForServices polls ServiceStates until every named service is "running".
func waitForServices(t *testing.T, d *daemon.Daemon, names []string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		states := d.ServiceStates()
		running := 0
		for _, s := range states {
			for _, name := range names {
				if s.Name == name && s.State == "running" {
					running++
				}
			}
		}
		if running == len(names) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	// Print final state for debugging before failing.
	for _, s := range d.ServiceStates() {
		t.Logf("service %s: state=%s health=%s port=%d", s.Name, s.State, s.Health, s.Port)
	}
	t.Fatalf("timed out waiting for services to reach running state")
}

func httpGet(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	return string(body)
}

func writeSpec(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
