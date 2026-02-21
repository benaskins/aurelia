package driver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/benaskins/aurelia/internal/logbuf"
)

// ContainerConfig holds configuration for a Docker container.
type ContainerConfig struct {
	Name        string
	Image       string
	Env         []string
	Cmd         []string          // command/args to pass to the container
	NetworkMode string            // "host", "bridge", etc. Default: "host"
	Volumes     map[string]string // host:container mount mappings
	BufSize     int               // log ring buffer size (lines)
}

// ContainerDriver manages a Docker container lifecycle.
type ContainerDriver struct {
	cfg ContainerConfig

	mu          sync.Mutex
	closeOnce   sync.Once
	client      *dockerclient.Client
	containerID string
	state       State
	startedAt   time.Time
	exitCode    int
	exitErr     string
	buf         *logbuf.Ring
	done        chan struct{}
}

// NewContainer creates a new Docker container driver.
func NewContainer(cfg ContainerConfig) (*ContainerDriver, error) {
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}

	bufSize := cfg.BufSize
	if bufSize <= 0 {
		bufSize = 1000
	}

	if cfg.NetworkMode == "" {
		cfg.NetworkMode = "host"
	}

	return &ContainerDriver{
		cfg:    cfg,
		client: cli,
		state:  StateStopped,
		buf:    logbuf.New(bufSize),
	}, nil
}

func (d *ContainerDriver) Start(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.state == StateRunning || d.state == StateStarting {
		return fmt.Errorf("container already running")
	}

	d.state = StateStarting

	// Build container config
	containerName := fmt.Sprintf("aurelia-%s", d.cfg.Name)

	// Remove any existing container with the same name
	d.client.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})

	config := &container.Config{
		Image: d.cfg.Image,
		Env:   d.cfg.Env,
		Cmd:   d.cfg.Cmd,
	}

	hostConfig := &container.HostConfig{
		NetworkMode: container.NetworkMode(d.cfg.NetworkMode),
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyDisabled, // aurelia handles restarts
		},
	}

	// Volume mounts
	if len(d.cfg.Volumes) > 0 {
		binds := make([]string, 0, len(d.cfg.Volumes))
		for host, cont := range d.cfg.Volumes {
			binds = append(binds, fmt.Sprintf("%s:%s", host, cont))
		}
		hostConfig.Binds = binds
	}

	// Create container
	resp, err := d.client.ContainerCreate(ctx, config, hostConfig, nil, nil, containerName)
	if err != nil {
		d.state = StateFailed
		d.exitErr = err.Error()
		return fmt.Errorf("creating container: %w", err)
	}
	d.containerID = resp.ID

	// Start container
	if err := d.client.ContainerStart(ctx, d.containerID, container.StartOptions{}); err != nil {
		d.state = StateFailed
		d.exitErr = err.Error()
		// Clean up created container
		d.client.ContainerRemove(ctx, d.containerID, container.RemoveOptions{Force: true})
		return fmt.Errorf("starting container: %w", err)
	}

	d.state = StateRunning
	d.startedAt = time.Now()
	d.done = make(chan struct{})

	// Stream logs in background
	go d.streamLogs(ctx)

	// Wait for container exit in background
	go d.waitForExit()

	return nil
}

func (d *ContainerDriver) Stop(ctx context.Context, timeout time.Duration) error {
	d.mu.Lock()

	if d.state != StateRunning {
		d.mu.Unlock()
		return nil
	}

	d.state = StateStopping
	containerID := d.containerID
	d.mu.Unlock()

	// Docker stop sends SIGTERM and waits for timeout before SIGKILL
	timeoutSec := int(timeout.Seconds())
	stopOpts := container.StopOptions{Timeout: &timeoutSec}
	d.client.ContainerStop(ctx, containerID, stopOpts)

	// Wait for the exit goroutine to finish
	select {
	case <-d.done:
	case <-time.After(timeout + 10*time.Second):
		// Force remove if stuck
		d.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
	}

	// Remove the container
	d.client.ContainerRemove(context.Background(), containerID, container.RemoveOptions{})

	// Close the Docker client to release resources (idempotent via closeOnce)
	d.closeClient()

	return nil
}

func (d *ContainerDriver) closeClient() {
	d.closeOnce.Do(func() {
		d.client.Close()
	})
}

func (d *ContainerDriver) Info() ProcessInfo {
	d.mu.Lock()
	defer d.mu.Unlock()

	return ProcessInfo{
		State:     d.state,
		StartedAt: d.startedAt,
		ExitCode:  d.exitCode,
		Error:     d.exitErr,
	}
}

func (d *ContainerDriver) Wait() (int, error) {
	d.mu.Lock()
	done := d.done
	d.mu.Unlock()
	if done == nil {
		return -1, fmt.Errorf("container not started")
	}
	<-done

	d.mu.Lock()
	defer d.mu.Unlock()
	return d.exitCode, nil
}

func (d *ContainerDriver) LogLines(n int) []string {
	return d.buf.Last(n)
}

func (d *ContainerDriver) streamLogs(ctx context.Context) {
	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	}

	reader, err := d.client.ContainerLogs(ctx, d.containerID, opts)
	if err != nil {
		return
	}
	defer reader.Close()

	// Docker multiplexes stdout/stderr with 8-byte frame headers.
	// StdCopy strips those headers, writing clean output to the ring buffer.
	stdcopy.StdCopy(d.buf, d.buf, reader)
}

func (d *ContainerDriver) waitForExit() {
	statusCh, errCh := d.client.ContainerWait(
		context.Background(),
		d.containerID,
		container.WaitConditionNotRunning,
	)

	select {
	case err := <-errCh:
		d.mu.Lock()
		wasStopping := d.state == StateStopping
		if wasStopping {
			d.state = StateStopped
		} else {
			d.state = StateFailed
		}
		if err != nil {
			d.exitErr = err.Error()
		}
		close(d.done)
		d.mu.Unlock()
		// On natural exit (not triggered by Stop), close the client here since
		// Stop() will never be called to do it.
		if !wasStopping {
			d.closeClient()
		}

	case status := <-statusCh:
		d.mu.Lock()
		d.exitCode = int(status.StatusCode)
		wasStopping := d.state == StateStopping
		if wasStopping {
			d.state = StateStopped
		} else if status.StatusCode != 0 {
			d.state = StateFailed
		} else {
			d.state = StateStopped
		}
		if status.Error != nil {
			d.exitErr = status.Error.Message
		}
		close(d.done)
		d.mu.Unlock()
		// On natural exit (not triggered by Stop), close the client here since
		// Stop() will never be called to do it.
		if !wasStopping {
			d.closeClient()
		}
	}
}

// ContainerID returns the Docker container ID (for external inspection).
func (d *ContainerDriver) ContainerID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.containerID
}
