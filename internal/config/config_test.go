package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadValidConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `routing_output: /tmp/traefik/dynamic.yaml
api_addr: 127.0.0.1:9090
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RoutingOutput != "/tmp/traefik/dynamic.yaml" {
		t.Errorf("RoutingOutput = %q, want %q", cfg.RoutingOutput, "/tmp/traefik/dynamic.yaml")
	}
	if cfg.APIAddr != "127.0.0.1:9090" {
		t.Errorf("APIAddr = %q, want %q", cfg.APIAddr, "127.0.0.1:9090")
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()
	cfg, err := Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if cfg.RoutingOutput != "" {
		t.Errorf("RoutingOutput = %q, want empty", cfg.RoutingOutput)
	}
	if cfg.APIAddr != "" {
		t.Errorf("APIAddr = %q, want empty", cfg.APIAddr)
	}
}

func TestLoadEmptyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RoutingOutput != "" {
		t.Errorf("RoutingOutput = %q, want empty", cfg.RoutingOutput)
	}
	if cfg.APIAddr != "" {
		t.Errorf("APIAddr = %q, want empty", cfg.APIAddr)
	}
}

func TestLoadPartialConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `routing_output: /tmp/traefik/dynamic.yaml
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RoutingOutput != "/tmp/traefik/dynamic.yaml" {
		t.Errorf("RoutingOutput = %q, want %q", cfg.RoutingOutput, "/tmp/traefik/dynamic.yaml")
	}
	if cfg.APIAddr != "" {
		t.Errorf("APIAddr = %q, want empty", cfg.APIAddr)
	}
}

func TestLoadCommentsOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `# routing_output: /tmp/traefik/dynamic.yaml
# api_addr: 127.0.0.1:9090
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RoutingOutput != "" {
		t.Errorf("RoutingOutput = %q, want empty", cfg.RoutingOutput)
	}
	if cfg.APIAddr != "" {
		t.Errorf("APIAddr = %q, want empty", cfg.APIAddr)
	}
}
