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

// Config holds persistent daemon configuration loaded from ~/.aurelia/config.yaml.
type Config struct {
	RoutingOutput string   `yaml:"routing_output"`
	APIAddr       string   `yaml:"api_addr"`
	NodeName      string   `yaml:"node_name,omitempty"`
	Nodes         []Node   `yaml:"nodes,omitempty"`
	LaminaRoot    string   `yaml:"lamina_root,omitempty"`
	TLS           *TLS     `yaml:"tls,omitempty"`
	OpenBao       *OpenBao `yaml:"openbao,omitempty"`
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
	return cfg, nil
}
