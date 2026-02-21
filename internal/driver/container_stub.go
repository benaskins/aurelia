//go:build nocontainer

package driver

import (
	"context"
	"fmt"
	"io"
	"time"
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

// ContainerDriver is a stub when container support is excluded.
type ContainerDriver struct{}

// NewContainer returns an error when built with the nocontainer tag.
func NewContainer(cfg ContainerConfig) (*ContainerDriver, error) {
	return nil, fmt.Errorf("container support excluded (built with nocontainer tag)")
}

func (d *ContainerDriver) Start(ctx context.Context) error {
	return fmt.Errorf("container support excluded")
}
func (d *ContainerDriver) Stop(ctx context.Context, _ time.Duration) error { return nil }
func (d *ContainerDriver) Info() ProcessInfo                               { return ProcessInfo{} }
func (d *ContainerDriver) Wait() (int, error)                              { return -1, fmt.Errorf("container support excluded") }
func (d *ContainerDriver) Stdout() io.Reader                               { return nil }
func (d *ContainerDriver) LogLines(n int) []string                         { return nil }
func (d *ContainerDriver) ContainerID() string                             { return "" }
