package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
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

// Status returns the raw JSON service states from the remote daemon.
// Callers decode into their own type to avoid import cycles.
func (c *Client) Status() (json.RawMessage, error) {
	body, err := c.get("/v1/services")
	if err != nil {
		return nil, err
	}
	defer body.Close()

	data, err := io.ReadAll(io.LimitReader(body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("reading status from %s: %w", c.Name, err)
	}
	return json.RawMessage(data), nil
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

// Ship triggers the fetch → build → deploy → notify pipeline on the remote daemon.
func (c *Client) Ship(name string) (json.RawMessage, error) {
	body, err := c.postReturnBody("/v1/services/" + name + "/ship")
	if err != nil {
		return nil, err
	}
	defer body.Close()

	data, err := io.ReadAll(io.LimitReader(body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("reading ship result from %s: %w", c.Name, err)
	}
	return json.RawMessage(data), nil
}

// Inspect returns the raw JSON inspect response for a service on the remote daemon.
func (c *Client) Inspect(name string) (json.RawMessage, error) {
	body, err := c.get("/v1/services/" + name + "/inspect")
	if err != nil {
		return nil, err
	}
	defer body.Close()

	data, err := io.ReadAll(io.LimitReader(body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("reading inspect from %s: %w", c.Name, err)
	}
	return json.RawMessage(data), nil
}

// LaminaResponse is the response from a remote lamina command execution.
type LaminaResponse struct {
	ExitCode int              `json:"exit_code"`
	Output   json.RawMessage  `json:"output,omitempty"`
	Raw      string           `json:"raw,omitempty"`
	Error    string           `json:"error,omitempty"`
}

// Lamina executes a lamina CLI command on the remote daemon.
// The args are the subcommand and its arguments, e.g. ["repo", "fetch"].
func (c *Client) Lamina(args []string) (*LaminaResponse, error) {
	body, err := c.postJSON("/v1/lamina", map[string]any{"args": args})
	if err != nil {
		return nil, err
	}
	defer body.Close()

	var resp LaminaResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decoding lamina response from %s: %w", c.Name, err)
	}
	return &resp, nil
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

func (c *Client) postReturnBody(path string) (io.ReadCloser, error) {
	req, err := http.NewRequest("POST", "http://"+c.addr+path, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request for %s: %w", c.Name, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s (%s): %w", c.Name, c.addr, err)
	}

	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		return nil, fmt.Errorf("%s returned %d: %s", c.Name, resp.StatusCode, body)
	}

	return resp.Body, nil
}

func (c *Client) postJSON(path string, v any) (io.ReadCloser, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshaling request for %s: %w", c.Name, err)
	}
	req, err := http.NewRequest("POST", "http://"+c.addr+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("creating request for %s: %w", c.Name, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s (%s): %w", c.Name, c.addr, err)
	}

	// For lamina exec, 422 carries valid response data (non-zero exit).
	// Only treat 4xx/5xx as errors when the body isn't a structured response.
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		return nil, fmt.Errorf("%s returned %d: %s", c.Name, resp.StatusCode, body)
	}

	return resp.Body, nil
}
