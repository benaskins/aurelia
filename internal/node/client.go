package node

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/benaskins/aurelia/internal/daemon"
)

// Client talks to a remote aurelia daemon over its TCP API.
type Client struct {
	Name  string // node name for display
	addr  string // host:port
	token string
	http  *http.Client
}

// New creates a client for a remote aurelia daemon.
func New(name, addr, token string) *Client {
	return &Client{
		Name:  name,
		addr:  addr,
		token: token,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Health checks if the remote daemon is reachable.
func (c *Client) Health() error {
	_, err := c.get("/v1/health")
	return err
}

// Status returns the service states from the remote daemon.
func (c *Client) Status() ([]daemon.ServiceState, error) {
	body, err := c.get("/v1/services")
	if err != nil {
		return nil, err
	}
	defer body.Close()

	var states []daemon.ServiceState
	if err := json.NewDecoder(body).Decode(&states); err != nil {
		return nil, fmt.Errorf("decoding status from %s: %w", c.Name, err)
	}
	return states, nil
}

// StartService starts a service on the remote daemon.
func (c *Client) StartService(name string) error {
	return c.post("/v1/services/" + name + "/start")
}

// StopService stops a service on the remote daemon.
func (c *Client) StopService(name string) error {
	return c.post("/v1/services/" + name + "/stop")
}

// RestartService restarts a service on the remote daemon.
func (c *Client) RestartService(name string) error {
	return c.post("/v1/services/" + name + "/restart")
}

// DeployService triggers a blue-green deploy on the remote daemon.
func (c *Client) DeployService(name string) error {
	return c.post("/v1/services/" + name + "/deploy")
}

// ReloadService triggers a spec reload on the remote daemon.
func (c *Client) ReloadService() error {
	return c.post("/v1/reload")
}

// Logs returns the last n log lines for a service on the remote daemon.
func (c *Client) Logs(name string, n int) ([]string, error) {
	body, err := c.get("/v1/services/" + name + "/logs?n=" + strconv.Itoa(n))
	if err != nil {
		return nil, err
	}
	defer body.Close()

	var resp struct {
		Lines []string `json:"lines"`
	}
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decoding logs from %s: %w", c.Name, err)
	}
	return resp.Lines, nil
}

func (c *Client) get(path string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", "http://"+c.addr+path, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request for %s: %w", c.Name, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s (%s): %w", c.Name, c.addr, err)
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		return nil, fmt.Errorf("%s returned %d: %s", c.Name, resp.StatusCode, body)
	}

	return resp.Body, nil
}

func (c *Client) post(path string) error {
	req, err := http.NewRequest("POST", "http://"+c.addr+path, nil)
	if err != nil {
		return fmt.Errorf("creating request for %s: %w", c.Name, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to %s (%s): %w", c.Name, c.addr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("%s returned %d: %s", c.Name, resp.StatusCode, body)
	}

	return nil
}
