package keychain

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// BaoStore implements Store backed by an OpenBao KV v1 secrets engine.
type BaoStore struct {
	addr       string
	token      string
	mount      string
	unsealFile string
	client     *http.Client
}

// BaoOption configures a BaoStore.
type BaoOption func(*BaoStore)

// WithUnsealFile configures auto-unseal using the key in the given file.
func WithUnsealFile(path string) BaoOption {
	return func(s *BaoStore) {
		s.unsealFile = path
	}
}

// NewBaoStore creates a store backed by OpenBao KV v1.
func NewBaoStore(addr, token, mount string, opts ...BaoOption) *BaoStore {
	s := &BaoStore{
		addr:  strings.TrimRight(addr, "/"),
		token: token,
		mount: mount,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// PKIIssuer returns a BaoPKIIssuer for the given mount, reusing this
// store's address and token.
func (s *BaoStore) PKIIssuer(mount string) *BaoPKIIssuer {
	pkiStore := NewBaoStore(s.addr, s.token, mount)
	return &BaoPKIIssuer{store: pkiStore, mount: mount}
}

func (s *BaoStore) do(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, s.addr+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", s.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return s.client.Do(req)
}

// Get retrieves a secret value from KV v1.
func (s *BaoStore) Get(key string) (string, error) {
	resp, err := s.do("GET", fmt.Sprintf("/v1/%s/%s", s.mount, key), nil)
	if err != nil {
		return "", fmt.Errorf("openbao get %s: %w", key, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openbao get %s: status %d", key, resp.StatusCode)
	}

	var result struct {
		Data struct {
			Value string `json:"value"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("openbao get %s: decode: %w", key, err)
	}
	return result.Data.Value, nil
}

// Set stores a secret value in KV v1.
func (s *BaoStore) Set(key, value string) error {
	body := strings.NewReader(fmt.Sprintf(`{"value":%q}`, value))
	resp, err := s.do("PUT", fmt.Sprintf("/v1/%s/%s", s.mount, key), body)
	if err != nil {
		return fmt.Errorf("openbao set %s: %w", key, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("openbao set %s: status %d", key, resp.StatusCode)
	}
	return nil
}

// Delete removes a secret from KV v1.
func (s *BaoStore) Delete(key string) error {
	resp, err := s.do("DELETE", fmt.Sprintf("/v1/%s/%s", s.mount, key), nil)
	if err != nil {
		return fmt.Errorf("openbao delete %s: %w", key, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("openbao delete %s: status %d", key, resp.StatusCode)
	}
	return nil
}

// List returns all secret keys under the mount path.
func (s *BaoStore) List() ([]string, error) {
	resp, err := s.do("LIST", fmt.Sprintf("/v1/%s/", s.mount), nil)
	if err != nil {
		return nil, fmt.Errorf("openbao list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openbao list: status %d", resp.StatusCode)
	}

	var result struct {
		Data struct {
			Keys []string `json:"keys"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("openbao list: decode: %w", err)
	}

	// Flatten: LIST returns folder prefixes (e.g. "aurelia/") alongside keys.
	// For flat key listing, filter out trailing slashes.
	var keys []string
	for _, k := range result.Data.Keys {
		if !strings.HasSuffix(k, "/") {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

// Ping checks that OpenBao is reachable and unsealed.
// If the server is sealed and an unseal key file is configured, it attempts auto-unseal.
func (s *BaoStore) Ping() error {
	resp, err := s.do("GET", "/v1/sys/health", nil)
	if err != nil {
		return fmt.Errorf("openbao health: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusServiceUnavailable: // 503 = sealed
		if s.unsealFile == "" {
			return fmt.Errorf("openbao is sealed and no unseal key configured")
		}
		if err := s.autoUnseal(); err != nil {
			return fmt.Errorf("openbao auto-unseal: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("openbao health: status %d", resp.StatusCode)
	}
}

func (s *BaoStore) autoUnseal() error {
	data, err := os.ReadFile(s.unsealFile)
	if err != nil {
		return fmt.Errorf("reading unseal key: %w", err)
	}
	key := strings.TrimSpace(string(data))

	body := strings.NewReader(fmt.Sprintf(`{"key":%q}`, key))
	resp, err := s.do("PUT", "/v1/sys/unseal", body)
	if err != nil {
		return fmt.Errorf("unseal request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unseal: status %d", resp.StatusCode)
	}

	var result struct {
		Sealed bool `json:"sealed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("unseal response: %w", err)
	}
	if result.Sealed {
		return fmt.Errorf("still sealed after unseal attempt")
	}
	return nil
}
