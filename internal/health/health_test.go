package health

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	neturl "net/url"
	"sync/atomic"
	"testing"
	"time"
)

func FuzzHealthCheckPath(f *testing.F) {
	f.Add("/health")
	f.Add("/")
	f.Add("/a/b/c?q=1")
	f.Add("/@redirect")
	f.Add("")
	f.Fuzz(func(t *testing.T, path string) {
		// Construct URL the same way the monitor does (see checkHTTP in health.go)
		url := fmt.Sprintf("http://127.0.0.1:%d%s", 8080, path)
		// Parsing shouldn't panic
		parsed, err := neturl.Parse(url)
		if err != nil {
			return
		}
		// If the path doesn't start with /, it can alter the URL authority
		// (e.g. path "@evil" makes "http://127.0.0.1:8080@evil").
		// The spec validator already requires paths start with /, so we only
		// check the invariant for well-formed paths.
		if len(path) > 0 && path[0] == '/' {
			if parsed.Hostname() != "127.0.0.1" {
				t.Errorf("health URL host changed to %q for path %q", parsed.Hostname(), path)
			}
		}
	})
}

func testLogger() *slog.Logger {
	return slog.Default().With("test", true)
}

func TestHTTPHealthCheck(t *testing.T) {
	// Start a test HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)
	defer srv.Close()

	cfg := Config{
		Type:               "http",
		Path:               "/health",
		Port:               port,
		Interval:           100 * time.Millisecond,
		Timeout:            2 * time.Second,
		UnhealthyThreshold: 3,
	}

	m := NewMonitor(cfg, testLogger(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.Start(ctx)
	time.Sleep(300 * time.Millisecond)
	m.Stop()

	if m.CurrentStatus() != StatusHealthy {
		t.Errorf("expected healthy, got %v", m.CurrentStatus())
	}

	result := m.LastResult()
	if result == nil {
		t.Fatal("expected a result")
	}
	if result.Status != StatusHealthy {
		t.Errorf("expected healthy result, got %v", result.Status)
	}
}

func TestHTTPHealthCheckUnhealthy(t *testing.T) {
	// Server that returns 500
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)
	defer srv.Close()

	var unhealthyCalled atomic.Bool

	cfg := Config{
		Type:               "http",
		Path:               "/health",
		Port:               port,
		Interval:           50 * time.Millisecond,
		Timeout:            2 * time.Second,
		UnhealthyThreshold: 2,
	}

	m := NewMonitor(cfg, testLogger(), func() {
		unhealthyCalled.Store(true)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.Start(ctx)
	time.Sleep(300 * time.Millisecond)
	m.Stop()

	if m.CurrentStatus() != StatusUnhealthy {
		t.Errorf("expected unhealthy, got %v", m.CurrentStatus())
	}

	if !unhealthyCalled.Load() {
		t.Error("expected onUnhealthy callback to fire")
	}
}

func TestTCPHealthCheck(t *testing.T) {
	// Start a TCP listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	// Accept connections in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	cfg := Config{
		Type:               "tcp",
		Port:               port,
		Interval:           100 * time.Millisecond,
		Timeout:            2 * time.Second,
		UnhealthyThreshold: 3,
	}

	m := NewMonitor(cfg, testLogger(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.Start(ctx)
	time.Sleep(300 * time.Millisecond)
	m.Stop()

	if m.CurrentStatus() != StatusHealthy {
		t.Errorf("expected healthy, got %v", m.CurrentStatus())
	}
}

func TestTCPHealthCheckUnhealthy(t *testing.T) {
	// Use a port nothing is listening on
	cfg := Config{
		Type:               "tcp",
		Port:               19999,
		Interval:           50 * time.Millisecond,
		Timeout:            100 * time.Millisecond,
		UnhealthyThreshold: 2,
	}

	m := NewMonitor(cfg, testLogger(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.Start(ctx)
	time.Sleep(300 * time.Millisecond)
	m.Stop()

	if m.CurrentStatus() != StatusUnhealthy {
		t.Errorf("expected unhealthy, got %v", m.CurrentStatus())
	}
}

func TestExecHealthCheck(t *testing.T) {
	cfg := Config{
		Type:               "exec",
		Command:            "true",
		Interval:           100 * time.Millisecond,
		Timeout:            2 * time.Second,
		UnhealthyThreshold: 3,
	}

	m := NewMonitor(cfg, testLogger(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.Start(ctx)
	time.Sleep(300 * time.Millisecond)
	m.Stop()

	if m.CurrentStatus() != StatusHealthy {
		t.Errorf("expected healthy, got %v", m.CurrentStatus())
	}
}

func TestExecHealthCheckUnhealthy(t *testing.T) {
	cfg := Config{
		Type:               "exec",
		Command:            "false",
		Interval:           50 * time.Millisecond,
		Timeout:            2 * time.Second,
		UnhealthyThreshold: 2,
	}

	m := NewMonitor(cfg, testLogger(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.Start(ctx)
	time.Sleep(300 * time.Millisecond)
	m.Stop()

	if m.CurrentStatus() != StatusUnhealthy {
		t.Errorf("expected unhealthy, got %v", m.CurrentStatus())
	}
}

func TestGracePeriod(t *testing.T) {
	cfg := Config{
		Type:               "exec",
		Command:            "true",
		Interval:           50 * time.Millisecond,
		Timeout:            2 * time.Second,
		GracePeriod:        200 * time.Millisecond,
		UnhealthyThreshold: 3,
	}

	m := NewMonitor(cfg, testLogger(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.Start(ctx)

	// Before grace period expires, status should be unknown
	time.Sleep(100 * time.Millisecond)
	if m.CurrentStatus() != StatusUnknown {
		t.Errorf("expected unknown during grace period, got %v", m.CurrentStatus())
	}

	// After grace period + first check
	time.Sleep(200 * time.Millisecond)
	m.Stop()

	if m.CurrentStatus() != StatusHealthy {
		t.Errorf("expected healthy after grace period, got %v", m.CurrentStatus())
	}
}

func TestUnhealthyThreshold(t *testing.T) {
	// Server that fails after 2 successful checks
	var checkCount atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		n := checkCount.Add(1)
		if n <= 2 {
			w.WriteHeader(200)
		} else {
			w.WriteHeader(500)
		}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)
	defer srv.Close()

	cfg := Config{
		Type:               "http",
		Path:               "/health",
		Port:               port,
		Interval:           50 * time.Millisecond,
		Timeout:            2 * time.Second,
		UnhealthyThreshold: 3,
	}

	m := NewMonitor(cfg, testLogger(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.Start(ctx)

	// First 2 checks succeed — should be healthy
	time.Sleep(130 * time.Millisecond)
	if m.CurrentStatus() != StatusHealthy {
		t.Errorf("expected healthy after first checks, got %v", m.CurrentStatus())
	}

	// Wait for threshold failures (3 more checks at 50ms each)
	time.Sleep(250 * time.Millisecond)
	m.Stop()

	if m.CurrentStatus() != StatusUnhealthy {
		t.Errorf("expected unhealthy after threshold failures, got %v (checks: %d)", m.CurrentStatus(), checkCount.Load())
	}
}

func TestRecoveryFromUnhealthy(t *testing.T) {
	// Use a channel to control when the server starts returning healthy
	var healthy atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			w.WriteHeader(200)
		} else {
			w.WriteHeader(500)
		}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)
	defer srv.Close()

	cfg := Config{
		Type:               "http",
		Path:               "/health",
		Port:               port,
		Interval:           50 * time.Millisecond,
		Timeout:            2 * time.Second,
		UnhealthyThreshold: 2,
	}

	m := NewMonitor(cfg, testLogger(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.Start(ctx)

	// Wait for unhealthy (threshold 2 at 50ms interval = ~150ms)
	time.Sleep(200 * time.Millisecond)
	if m.CurrentStatus() != StatusUnhealthy {
		t.Errorf("expected unhealthy, got %v", m.CurrentStatus())
	}

	// Switch to healthy
	healthy.Store(true)

	// Wait for recovery (one successful check resets to healthy)
	time.Sleep(200 * time.Millisecond)
	m.Stop()

	if m.CurrentStatus() != StatusHealthy {
		t.Errorf("expected recovery to healthy, got %v", m.CurrentStatus())
	}
}

func TestResultDuration(t *testing.T) {
	cfg := Config{
		Type:               "exec",
		Command:            fmt.Sprintf("sleep 0.05"),
		Interval:           200 * time.Millisecond,
		Timeout:            2 * time.Second,
		UnhealthyThreshold: 3,
	}

	m := NewMonitor(cfg, testLogger(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.Start(ctx)
	time.Sleep(300 * time.Millisecond)
	m.Stop()

	result := m.LastResult()
	if result == nil {
		t.Fatal("expected a result")
	}
	if result.Duration < 40*time.Millisecond {
		t.Errorf("expected duration >= 40ms, got %v", result.Duration)
	}
}
