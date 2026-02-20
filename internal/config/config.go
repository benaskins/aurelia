package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds persistent daemon configuration loaded from ~/.aurelia/config.yaml.
type Config struct {
	RoutingOutput string `yaml:"routing_output"`
	APIAddr       string `yaml:"api_addr"`
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
	return cfg, nil
}
