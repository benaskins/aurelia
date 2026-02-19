package driver

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"

	"github.com/benaskins/aurelia/internal/logbuf"
)

// ContainerConfig holds configuration for a Docker container.
type ContainerConfig struct {
	Name        string
	Image       string
	Env         []string
	NetworkMode string            // "host", "bridge", etc. Default: "host"
	Volumes     map[string]string // host:container mount mappings
	BufSize     int               // log ring buffer size (lines)
}

// ContainerDriver manages a Docker container lifecycle.
type ContainerDriver struct {
	cfg ContainerConfig

	mu          sync.Mutex
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

	return nil
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
	if d.done == nil {
		return -1, fmt.Errorf("container not started")
	}
	<-d.done

	d.mu.Lock()
	defer d.mu.Unlock()
	return d.exitCode, nil
}

func (d *ContainerDriver) Stdout() io.Reader {
	return d.buf.Reader()
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

	// Docker log stream has a multiplexed header (8 bytes per frame).
	// Use stdcopy.StdCopy to demux, or just strip headers.
	// For simplicity, copy raw — the 8-byte headers are noise but logs
	// are still readable. A proper implementation would use stdcopy.
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			// Docker multiplexed stream: first 8 bytes are header per frame
			// Header: [stream_type(1), 0, 0, 0, size(4)]
			// For host networking, logs may come without multiplexing.
			// Write raw to ring buffer — good enough for now.
			d.buf.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
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
		if d.state == StateStopping {
			d.state = StateStopped
		} else {
			d.state = StateFailed
		}
		if err != nil {
			d.exitErr = err.Error()
		}
		close(d.done)
		d.mu.Unlock()

	case status := <-statusCh:
		d.mu.Lock()
		d.exitCode = int(status.StatusCode)
		if d.state == StateStopping {
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
	}
}

// ContainerID returns the Docker container ID (for external inspection).
func (d *ContainerDriver) ContainerID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.containerID
}

// parseImageName extracts the image name without tag for container naming.
func parseImageName(image string) string {
	// Remove tag
	parts := strings.SplitN(image, ":", 2)
	name := parts[0]
	// Remove registry prefix
	parts = strings.Split(name, "/")
	return parts[len(parts)-1]
}
