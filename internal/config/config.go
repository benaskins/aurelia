package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Node represents a remote aurelia daemon peer.
type Node struct {
	Name      string `yaml:"name"`
	Addr      string `yaml:"addr"`                // e.g. "aurelia.local:9090"
	Token     string `yaml:"token,omitempty"`      // inline token
	TokenFile string `yaml:"token_file,omitempty"` // path to token file
}

// LoadToken returns the bearer token for this node, reading from file if needed.
func (n Node) LoadToken() (string, error) {
	if n.Token != "" {
		return n.Token, nil
	}
	if n.TokenFile != "" {
		data, err := os.ReadFile(n.TokenFile)
		if err != nil {
			return "", fmt.Errorf("reading token file for node %s: %w", n.Name, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return "", fmt.Errorf("no token configured for node %s", n.Name)
}

// TLS holds TLS certificate paths for the daemon's TCP listener and peer connections.
type TLS struct {
	Cert string `yaml:"cert"` // path to server certificate (PEM)
	Key  string `yaml:"key"`  // path to server private key (PEM)
	CA   string `yaml:"ca"`   // path to CA certificate for verifying client certs (PEM)
}

// Configured returns true if all required TLS paths are set.
func (t *TLS) Configured() bool {
	return t != nil && t.Cert != "" && t.Key != "" && t.CA != ""
}

// OpenBao configures the OpenBao secrets backend.
type OpenBao struct {
	Addr       string `yaml:"addr"`
	TokenFile  string `yaml:"token_file"`
	Mount      string `yaml:"mount"`
	UnsealFile string `yaml:"unseal_file,omitempty"`
}

// LoadToken reads the OpenBao token from the configured file,
// falling back to the BAO_TOKEN environment variable.
func (o OpenBao) LoadToken() (string, error) {
	if o.TokenFile != "" {
		data, err := os.ReadFile(o.TokenFile)
		if err == nil {
			return strings.TrimSpace(string(data)), nil
		}
	}
	if t := os.Getenv("BAO_TOKEN"); t != "" {
		return t, nil
	}
	return "", fmt.Errorf("no openbao token: set token_file in config or BAO_TOKEN env var")
}

// OpenBaoPeer configures secret reads via token vending from a peer node.
// The daemon vends a short-lived token from the peer, then reads from
// OpenBao directly using that scoped token.
type OpenBaoPeer struct {
	Peer  string `yaml:"peer"`  // node name that vends tokens (e.g. "adyton")
	Addr  string `yaml:"addr"`  // openbao API address (e.g. "http://openbao.adyton.internal")
	Mount string `yaml:"mount"` // KV mount path (default "secret")
}

// Diagnose configures the LLM-powered diagnostic engine.
type Diagnose struct {
	Provider     string `yaml:"provider"`        // LLM provider: "anthropic", "openai"
	Model        string `yaml:"model"`           // model name, e.g. "claude-sonnet-4-20250514"
	APIKeySecret string `yaml:"api_key_secret"`  // secret name for the API key (resolved via aurelia secret)
	BaseURL      string `yaml:"base_url,omitempty"` // base URL for openai-compatible providers
}

// ServiceCertConfig describes a TLS certificate to auto-renew via the CA peer.
type ServiceCertConfig struct {
	Role     string `yaml:"role"`      // PKI role (server, client)
	CN       string `yaml:"cn"`        // common name
	TTL      string `yaml:"ttl"`       // requested TTL (e.g. "720h", "24h")
	CertDir  string `yaml:"cert_dir"`  // directory for cert files
	IsClient bool   `yaml:"is_client"` // true for client certs (client.crt), false for server (cert.crt)
}

// Config holds persistent daemon configuration loaded from ~/.aurelia/config.yaml.
type Config struct {
	RoutingOutput string    `yaml:"routing_output"`
	APIAddr       string    `yaml:"api_addr"`
	NodeName      string    `yaml:"node_name,omitempty"`
	Nodes         []Node    `yaml:"nodes,omitempty"`
	LaminaRoot    string    `yaml:"lamina_root,omitempty"`
	SpecSource    string    `yaml:"spec_source,omitempty"` // source spec directory for drift detection
	TLS           *TLS                `yaml:"tls,omitempty"`
	OpenBao       *OpenBao            `yaml:"openbao,omitempty"`
	OpenBaoPeer   *OpenBaoPeer        `yaml:"openbao_peer,omitempty"`
	Diagnose      *Diagnose           `yaml:"diagnose,omitempty"`
	ServiceCerts  []ServiceCertConfig `yaml:"service_certs,omitempty"`
}

// SpecSourceDir returns the source spec directory for drift detection.
// Resolution order:
//  1. Explicit spec_source config field
//  2. ${AURELIA_ROOT}/aurelia/services/ (from environment or launchd plist)
//
// Returns empty string if the source directory cannot be determined.
func (c *Config) SpecSourceDir() string {
	if c.SpecSource != "" {
		return c.SpecSource
	}
	root := os.Getenv("AURELIA_ROOT")
	if root == "" {
		root = aureliaRootFromPlist()
	}
	if root != "" {
		return filepath.Join(root, "aurelia", "services")
	}
	return ""
}

// aureliaRootFromPlist reads AURELIA_ROOT from the launchd plist as a fallback
// when the environment variable is not set (e.g. in interactive shell sessions).
func aureliaRootFromPlist() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.aurelia.daemon.plist")
	data, err := os.ReadFile(plist)
	if err != nil {
		return ""
	}
	// Simple extraction: find AURELIA_ROOT key followed by a string value.
	// The plist format is:
	//   <key>AURELIA_ROOT</key>
	//   <string>/path/to/root</string>
	content := string(data)
	marker := "<key>AURELIA_ROOT</key>"
	idx := strings.Index(content, marker)
	if idx < 0 {
		return ""
	}
	rest := content[idx+len(marker):]
	startTag := "<string>"
	endTag := "</string>"
	start := strings.Index(rest, startTag)
	if start < 0 {
		return ""
	}
	rest = rest[start+len(startTag):]
	end := strings.Index(rest, endTag)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

// FindNode returns the node with the given name, or false if not found.
func (c *Config) FindNode(name string) (Node, bool) {
	for _, n := range c.Nodes {
		if n.Name == name {
			return n, true
		}
	}
	return Node{}, false
}

// UpdateNodeToken updates the inline token for a named node in the config file.
// Reads the file, modifies the node's token, and writes it back.
func UpdateNodeToken(path, nodeName, newToken string) error {
	cfg, err := Load(path)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	found := false
	for i := range cfg.Nodes {
		if cfg.Nodes[i].Name == nodeName {
			cfg.Nodes[i].Token = newToken
			cfg.Nodes[i].TokenFile = "" // clear file reference, inline takes precedence
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("node %q not found in config", nodeName)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// DefaultPath returns the default config file path: ~/.aurelia/config.yaml.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".aurelia", "config.yaml")
}

// Load reads a YAML config file from path. If the file does not exist,
// it returns an empty Config and no error. An empty or all-comment file
// also returns an empty Config with no error.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, err
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	cfg.RoutingOutput = os.ExpandEnv(cfg.RoutingOutput)
	cfg.APIAddr = os.ExpandEnv(cfg.APIAddr)
	cfg.LaminaRoot = os.ExpandEnv(cfg.LaminaRoot)
	cfg.SpecSource = os.ExpandEnv(cfg.SpecSource)
	return cfg, nil
}
