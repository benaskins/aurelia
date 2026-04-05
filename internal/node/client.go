package node

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Client talks to a remote aurelia daemon over its TCP API.
type Client struct {
	Name   string // node name for display
	addr   string // host:port
	token  string
	scheme string // "http" or "https"
	http   *http.Client
}

// peerTransport returns an http.Transport configured for peer communication.
// Short idle timeout and connection lifetime ensure DNS changes (e.g., after
// a network partition recovery) are picked up within one liveness cycle.
func peerTransport(tlsConfig *tls.Config) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 15 * time.Second,
	}
	t := &http.Transport{
		DialContext:         dialer.DialContext,
		IdleConnTimeout:     15 * time.Second,
		MaxIdleConnsPerHost: 1,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	if tlsConfig != nil {
		t.TLSClientConfig = tlsConfig
	}
	return t
}

// New creates a client for a remote aurelia daemon using bearer token auth over plain HTTP.
func New(name, addr, token string) *Client {
	return &Client{
		Name:   name,
		addr:   addr,
		token:  token,
		scheme: "http",
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: peerTransport(nil),
		},
	}
}

// NewTLS creates a client that connects over TLS with a client certificate (mTLS).
// The token is still set for backward compatibility but is not sent when a client cert
// is configured (the server authenticates via the cert instead).
func NewTLS(name, addr, token string, tlsConfig *tls.Config) *Client {
	return &Client{
		Name:   name,
		addr:   addr,
		token:  token,
		scheme: "https",
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: peerTransport(tlsConfig),
		},
	}
}

// Health checks if the remote daemon is reachable.
func (c *Client) Health() error {
	_, err := c.get("/v1/health")
	return err
}

// CloseIdleConnections closes idle HTTP connections in the client's pool.
// Call after a failed health check to force fresh DNS resolution and TCP
// connections on the next attempt.
func (c *Client) CloseIdleConnections() {
	c.http.CloseIdleConnections()
}

// Status returns the raw JSON service states from the remote daemon.
// Callers decode into their own type to avoid import cycles.
func (c *Client) Status() (json.RawMessage, error) {
	return c.StatusContext(context.Background())
}

// StatusContext returns service states, respecting the provided context deadline.
func (c *Client) StatusContext(ctx context.Context) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.scheme+"://"+c.addr+"/v1/services", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request for %s: %w", c.Name, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s (%s): %w", c.Name, c.addr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("%s returned %d: %s", c.Name, resp.StatusCode, body)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("reading status from %s: %w", c.Name, err)
	}
	return json.RawMessage(data), nil
}

// GraphContext returns the service graph from the remote daemon.
func (c *Client) GraphContext(ctx context.Context) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.scheme+"://"+c.addr+"/v1/graph", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request for %s: %w", c.Name, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s (%s): %w", c.Name, c.addr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("%s returned %d: %s", c.Name, resp.StatusCode, body)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("reading graph from %s: %w", c.Name, err)
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
	ExitCode int             `json:"exit_code"`
	Output   json.RawMessage `json:"output,omitempty"`
	Raw      string          `json:"raw,omitempty"`
	Error    string          `json:"error,omitempty"`
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

// BaoTokenResponse is the response from an OpenBao token vend request.
type BaoTokenResponse struct {
	Token     string   `json:"token"`
	ExpiresAt string   `json:"expires_at"`
	Policies  []string `json:"policies"`
}

// RequestBaoToken requests a short-lived, scoped OpenBao token from the remote
// daemon's token vending endpoint. Requires mTLS authentication — the peer's
// cert CN determines which policy is applied.
func (c *Client) RequestBaoToken() (*BaoTokenResponse, error) {
	body, err := c.postReturnBody("/v1/openbao/token")
	if err != nil {
		return nil, err
	}
	defer body.Close()

	var resp BaoTokenResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decoding bao token from %s: %w", c.Name, err)
	}
	return &resp, nil
}

// RenewCertResponse is the response from a PKI cert renewal request.
type RenewCertResponse struct {
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
	CAChain     string `json:"ca_chain"`
	Serial      string `json:"serial_number"`
	Expiration  int64  `json:"expiration"`
}

// RenewCert requests a new mTLS certificate from the remote daemon's PKI
// renewal endpoint. Requires mTLS authentication — the peer's cert CN
// determines the common name on the issued certificate.
func (c *Client) RenewCert() (*RenewCertResponse, error) {
	body, err := c.postReturnBody("/v1/pki/renew")
	if err != nil {
		return nil, err
	}
	defer body.Close()

	var resp RenewCertResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decoding renewed cert from %s: %w", c.Name, err)
	}
	return &resp, nil
}

// IssueCert requests a certificate from the remote daemon's PKI engine.
// Requires mTLS authentication. The role determines the PKI role used
// (e.g. "server", "client", "node"). Returns the issued cert, key, and CA chain.
func (c *Client) IssueCert(role, commonName, ttl string) (*RenewCertResponse, error) {
	body, err := c.postJSON("/v1/pki/issue", map[string]string{
		"role":        role,
		"common_name": commonName,
		"ttl":         ttl,
	})
	if err != nil {
		return nil, err
	}
	defer body.Close()

	var resp RenewCertResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decoding issued cert from %s: %w", c.Name, err)
	}
	return &resp, nil
}

// RequestSecrets fetches all secrets from the remote daemon's bulk endpoint.
// Requires mTLS authentication. Returns a key→value map.
func (c *Client) RequestSecrets() (map[string]string, error) {
	body, err := c.get("/v1/secrets")
	if err != nil {
		return nil, err
	}
	defer body.Close()

	var secrets map[string]string
	if err := json.NewDecoder(body).Decode(&secrets); err != nil {
		return nil, fmt.Errorf("decoding secrets from %s: %w", c.Name, err)
	}
	return secrets, nil
}

// InvalidateCache pushes a cache invalidation to the remote daemon.
// If key is empty, all cached secrets are invalidated.
// Requires mTLS authentication.
func (c *Client) InvalidateCache(key string) error {
	_, err := c.postJSON("/v1/cache/invalidate", map[string]string{"key": key})
	return err
}

// PushToken sends a new bearer token to the remote peer for updating its config.
// This is used during token rotation and requires mTLS authentication.
func (c *Client) PushToken(nodeName, newToken string) error {
	_, err := c.postJSON("/v1/peer/token", map[string]string{
		"node":  nodeName,
		"token": newToken,
	})
	if err != nil {
		return err
	}
	return nil
}

func (c *Client) get(path string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", c.scheme+"://"+c.addr+path, nil)
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
	req, err := http.NewRequest("POST", c.scheme+"://"+c.addr+path, nil)
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
	req, err := http.NewRequest("POST", c.scheme+"://"+c.addr+path, nil)
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
	req, err := http.NewRequest("POST", c.scheme+"://"+c.addr+path, bytes.NewReader(data))
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
