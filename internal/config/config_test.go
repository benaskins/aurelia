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

func TestLoadExpandsEnvVars(t *testing.T) {
	t.Setenv("AURELIA_ROOT", "/opt/aurelia")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `routing_output: ${AURELIA_ROOT}/traefik/dynamic/aurelia.yaml
api_addr: 127.0.0.1:9090
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RoutingOutput != "/opt/aurelia/traefik/dynamic/aurelia.yaml" {
		t.Errorf("RoutingOutput = %q, want expanded path", cfg.RoutingOutput)
	}
	if cfg.APIAddr != "127.0.0.1:9090" {
		t.Errorf("APIAddr = %q, want unchanged", cfg.APIAddr)
	}
}

func TestLoadNodesConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `routing_output: /tmp/traefik/dynamic.yaml
api_addr: 127.0.0.1:9090
node_name: aurelia
nodes:
  - name: limen
    addr: limen.local:9090
    token: secret123
  - name: aurelia
    addr: aurelia.local:9090
    token_file: /tmp/aurelia.token
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.NodeName != "aurelia" {
		t.Errorf("NodeName = %q, want %q", cfg.NodeName, "aurelia")
	}
	if len(cfg.Nodes) != 2 {
		t.Fatalf("len(Nodes) = %d, want 2", len(cfg.Nodes))
	}
	if cfg.Nodes[0].Name != "limen" {
		t.Errorf("Nodes[0].Name = %q, want %q", cfg.Nodes[0].Name, "limen")
	}
	if cfg.Nodes[0].Addr != "limen.local:9090" {
		t.Errorf("Nodes[0].Addr = %q, want %q", cfg.Nodes[0].Addr, "limen.local:9090")
	}
	if cfg.Nodes[0].Token != "secret123" {
		t.Errorf("Nodes[0].Token = %q, want %q", cfg.Nodes[0].Token, "secret123")
	}
	if cfg.Nodes[1].TokenFile != "/tmp/aurelia.token" {
		t.Errorf("Nodes[1].TokenFile = %q, want %q", cfg.Nodes[1].TokenFile, "/tmp/aurelia.token")
	}
}

func TestFindNode(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Nodes: []Node{
			{Name: "aurelia", Addr: "aurelia.local:9090"},
			{Name: "limen", Addr: "limen.local:9090"},
		},
	}

	n, ok := cfg.FindNode("limen")
	if !ok {
		t.Fatal("expected to find node limen")
	}
	if n.Addr != "limen.local:9090" {
		t.Errorf("Addr = %q, want %q", n.Addr, "limen.local:9090")
	}

	_, ok = cfg.FindNode("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent node")
	}
}

func TestNodeLoadToken(t *testing.T) {
	t.Parallel()

	// Inline token
	n := Node{Name: "test", Token: "inline-token"}
	token, err := n.LoadToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "inline-token" {
		t.Errorf("token = %q, want %q", token, "inline-token")
	}

	// Token from file
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("file-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	n2 := Node{Name: "test2", TokenFile: tokenPath}
	token2, err := n2.LoadToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token2 != "file-token" {
		t.Errorf("token = %q, want %q", token2, "file-token")
	}

	// No token configured
	n3 := Node{Name: "test3"}
	_, err = n3.LoadToken()
	if err == nil {
		t.Error("expected error when no token configured")
	}
}

func TestLoadTLSConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `api_addr: 0.0.0.0:9090
tls:
  cert: /etc/aurelia/tls/server.crt
  key: /etc/aurelia/tls/server.key
  ca: /etc/aurelia/tls/ca.crt
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TLS == nil {
		t.Fatal("expected TLS config to be present")
	}
	if cfg.TLS.Cert != "/etc/aurelia/tls/server.crt" {
		t.Errorf("TLS.Cert = %q, want /etc/aurelia/tls/server.crt", cfg.TLS.Cert)
	}
	if cfg.TLS.Key != "/etc/aurelia/tls/server.key" {
		t.Errorf("TLS.Key = %q, want /etc/aurelia/tls/server.key", cfg.TLS.Key)
	}
	if cfg.TLS.CA != "/etc/aurelia/tls/ca.crt" {
		t.Errorf("TLS.CA = %q, want /etc/aurelia/tls/ca.crt", cfg.TLS.CA)
	}
	if !cfg.TLS.Configured() {
		t.Error("expected TLS.Configured() to return true")
	}
}

func TestTLSConfiguredPartial(t *testing.T) {
	t.Parallel()

	// Missing CA
	tls := &TLS{Cert: "/a.crt", Key: "/a.key"}
	if tls.Configured() {
		t.Error("expected Configured() = false when CA is missing")
	}

	// Nil
	var nilTLS *TLS
	if nilTLS.Configured() {
		t.Error("expected Configured() = false for nil TLS")
	}
}

func TestLoadNoTLSConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `api_addr: 127.0.0.1:9090
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TLS != nil {
		t.Errorf("expected TLS to be nil when not configured, got %+v", cfg.TLS)
	}
	if cfg.TLS.Configured() {
		t.Error("expected Configured() = false for absent TLS block")
	}
}

func TestUpdateNodeToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `node_name: hestia
nodes:
  - name: limen
    addr: limen.internal:9090
    token: old-token
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if err := UpdateNodeToken(path, "limen", "new-token"); err != nil {
		t.Fatalf("UpdateNodeToken: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after update: %v", err)
	}
	n, ok := cfg.FindNode("limen")
	if !ok {
		t.Fatal("node limen not found after update")
	}
	if n.Token != "new-token" {
		t.Errorf("token = %q, want %q", n.Token, "new-token")
	}
	if n.TokenFile != "" {
		t.Errorf("token_file should be cleared, got %q", n.TokenFile)
	}
}

func TestUpdateNodeTokenNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `node_name: hestia
nodes:
  - name: limen
    addr: limen.internal:9090
    token: tok
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := UpdateNodeToken(path, "nonexistent", "new-token")
	if err == nil {
		t.Error("expected error for nonexistent node")
	}
}

func TestLoadDiagnoseConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `diagnose:
  provider: anthropic
  model: claude-sonnet-4-20250514
  api_key_secret: anthropic-api-key
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Diagnose == nil {
		t.Fatal("expected Diagnose config to be present")
	}
	if cfg.Diagnose.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", cfg.Diagnose.Provider, "anthropic")
	}
	if cfg.Diagnose.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Model = %q, want %q", cfg.Diagnose.Model, "claude-sonnet-4-20250514")
	}
	if cfg.Diagnose.APIKeySecret != "anthropic-api-key" {
		t.Errorf("APIKeySecret = %q, want %q", cfg.Diagnose.APIKeySecret, "anthropic-api-key")
	}
}

func TestLoadNoDiagnoseConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `api_addr: 127.0.0.1:9090
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Diagnose != nil {
		t.Errorf("expected Diagnose to be nil when not configured, got %+v", cfg.Diagnose)
	}
}

func TestLoadSpecSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `spec_source: /opt/infra/aurelia/services
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SpecSource != "/opt/infra/aurelia/services" {
		t.Errorf("SpecSource = %q, want %q", cfg.SpecSource, "/opt/infra/aurelia/services")
	}
}

func TestLoadSpecSourceExpandsEnv(t *testing.T) {
	t.Setenv("MY_ROOT", "/opt/myroot")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `spec_source: ${MY_ROOT}/aurelia/services
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SpecSource != "/opt/myroot/aurelia/services" {
		t.Errorf("SpecSource = %q, want expanded path", cfg.SpecSource)
	}
}

func TestSpecSourceDir(t *testing.T) {
	// Explicit spec_source takes precedence
	t.Run("explicit", func(t *testing.T) {
		t.Setenv("AURELIA_ROOT", "/should/not/use")
		cfg := &Config{SpecSource: "/explicit/path"}
		if got := cfg.SpecSourceDir(); got != "/explicit/path" {
			t.Errorf("SpecSourceDir() = %q, want %q", got, "/explicit/path")
		}
	})

	// Falls back to AURELIA_ROOT env var
	t.Run("aurelia_root_env", func(t *testing.T) {
		t.Setenv("AURELIA_ROOT", "/opt/aurelia-infra")
		cfg := &Config{}
		want := "/opt/aurelia-infra/aurelia/services"
		if got := cfg.SpecSourceDir(); got != want {
			t.Errorf("SpecSourceDir() = %q, want %q", got, want)
		}
	})
}

func TestAureliaRootFromPlist(t *testing.T) {
	// This test verifies the plist parsing logic by checking that
	// aureliaRootFromPlist returns a non-empty value on macOS when
	// the launchd plist exists. On CI or other environments, it
	// gracefully returns empty.
	result := aureliaRootFromPlist()
	// We can't assert a specific value since it depends on the machine,
	// but we can verify it doesn't panic and returns a valid path or empty.
	if result != "" {
		if !filepath.IsAbs(result) {
			t.Errorf("aureliaRootFromPlist() = %q, expected absolute path", result)
		}
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
