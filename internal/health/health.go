package health

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

// Status represents the health state of a service.
type Status string

const (
	StatusUnknown   Status = "unknown"
	StatusHealthy   Status = "healthy"
	StatusUnhealthy Status = "unhealthy"
)

// Config holds health check configuration, mapped from the spec.
type Config struct {
	Type               string        // "http" | "tcp" | "exec"
	Path               string        // http only
	Port               int           // http and tcp
	Command            string        // exec only
	Interval           time.Duration // time between checks
	Timeout            time.Duration // max time per check
	GracePeriod        time.Duration // delay before first check
	UnhealthyThreshold int           // consecutive failures before unhealthy
}

// Result is the outcome of a single health check.
type Result struct {
	Status  Status
	Message string
}

// Monitor runs periodic health checks and tracks state.
type Monitor struct {
	cfg        Config
	logger     *slog.Logger
	httpClient *http.Client

	mu               sync.Mutex
	status           Status
	consecutiveFails int
	cancel           context.CancelFunc
	done             chan struct{}

	// onUnhealthy is called when the service transitions to unhealthy.
	onUnhealthy func()
}

// NewMonitor creates a health check monitor.
func NewMonitor(cfg Config, logger *slog.Logger, onUnhealthy func()) *Monitor {
	if cfg.UnhealthyThreshold <= 0 {
		cfg.UnhealthyThreshold = 3
	}
	return &Monitor{
		cfg:         cfg,
		logger:      logger,
		httpClient:  &http.Client{Timeout: cfg.Timeout},
		status:      StatusUnknown,
		onUnhealthy: onUnhealthy,
	}
}

// Start begins periodic health checking.
func (m *Monitor) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.cancel = cancel
	m.done = make(chan struct{})
	m.mu.Unlock()

	go m.run(ctx)
}

// Stop halts the health check loop.
func (m *Monitor) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	done := m.done
	m.mu.Unlock()

	if cancel != nil {
		cancel()
		<-done
	}
}

// Status returns the current health status.
func (m *Monitor) CurrentStatus() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *Monitor) run(ctx context.Context) {
	defer func() {
		m.mu.Lock()
		m.cancel = nil
		close(m.done)
		m.mu.Unlock()
	}()

	// Grace period
	if m.cfg.GracePeriod > 0 {
		select {
		case <-time.After(m.cfg.GracePeriod):
		case <-ctx.Done():
			return
		}
	}

	ticker := time.NewTicker(m.cfg.Interval)
	defer ticker.Stop()

	// Run first check immediately
	m.check(ctx)

	for {
		select {
		case <-ticker.C:
			m.check(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (m *Monitor) check(ctx context.Context) {
	checkCtx, cancel := context.WithTimeout(ctx, m.cfg.Timeout)
	defer cancel()

	var result Result

	var err error
	switch m.cfg.Type {
	case "http":
		err = m.checkHTTP(checkCtx)
	case "tcp":
		err = m.checkTCP(checkCtx)
	case "exec":
		err = m.checkExec(checkCtx)
	default:
		err = fmt.Errorf("unknown health check type: %s", m.cfg.Type)
	}

	// Don't record results from cancelled context — the monitor is shutting down
	if ctx.Err() != nil {
		return
	}

	if err != nil {
		result.Status = StatusUnhealthy
		result.Message = err.Error()
	} else {
		result.Status = StatusHealthy
		result.Message = "ok"
	}

	m.mu.Lock()
	prevStatus := m.status

	if result.Status == StatusHealthy {
		m.consecutiveFails = 0
		m.status = StatusHealthy
	} else {
		m.consecutiveFails++
		if m.consecutiveFails >= m.cfg.UnhealthyThreshold {
			m.status = StatusUnhealthy
		}
	}

	newStatus := m.status
	consecutiveFails := m.consecutiveFails
	m.mu.Unlock()

	if result.Status != StatusHealthy {
		m.logger.Warn("health check failed",
			"error", result.Message,
			"consecutive_fails", consecutiveFails,
			"threshold", m.cfg.UnhealthyThreshold,
		)
	}

	// Fire callback on transition to unhealthy
	if prevStatus != StatusUnhealthy && newStatus == StatusUnhealthy {
		m.logger.Error("service is unhealthy", "consecutive_fails", consecutiveFails)
		if m.onUnhealthy != nil {
			m.onUnhealthy()
		}
	}
}

// SingleCheck runs one health check with the given config and returns nil if healthy.
// Unlike Monitor, it does not track state or run periodically.
func SingleCheck(cfg Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	switch cfg.Type {
	case "http":
		return checkHTTP(ctx, cfg)
	case "tcp":
		return checkTCP(ctx, cfg)
	case "exec":
		return checkExec(ctx, cfg)
	default:
		return fmt.Errorf("unknown health check type: %s", cfg.Type)
	}
}

// checkHTTP performs a single HTTP health check (standalone version).
func checkHTTP(ctx context.Context, cfg Config) error {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", cfg.Port, cfg.Path)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	client := &http.Client{Timeout: cfg.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unhealthy status: %d", resp.StatusCode)
	}
	return nil
}

// checkTCP performs a single TCP health check (standalone version).
func checkTCP(ctx context.Context, cfg Config) error {
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	dialer := net.Dialer{Timeout: cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("tcp connect failed: %w", err)
	}
	conn.Close()
	return nil
}

// checkExec performs a single exec health check (standalone version).
func checkExec(ctx context.Context, cfg Config) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", cfg.Command)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command failed: %w", err)
	}
	return nil
}

func (m *Monitor) checkHTTP(ctx context.Context) error {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", m.cfg.Port, m.cfg.Path)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unhealthy status: %d", resp.StatusCode)
	}

	return nil
}

func (m *Monitor) checkTCP(ctx context.Context) error {
	addr := fmt.Sprintf("127.0.0.1:%d", m.cfg.Port)
	dialer := net.Dialer{Timeout: m.cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("tcp connect failed: %w", err)
	}
	conn.Close()
	return nil
}

func (m *Monitor) checkExec(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", m.cfg.Command)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command failed: %w", err)
	}
	return nil
}
