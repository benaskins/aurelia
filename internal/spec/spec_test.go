package spec

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadValidContainerSpec(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "chat.yaml")
	data := `
service:
  name: chat
  type: container
  image: chat:prod
  network_mode: host

network:
  port: 8090

health:
  type: http
  path: /health
  interval: 10s
  timeout: 2s
  grace_period: 5s
  unhealthy_threshold: 3

restart:
  policy: on-failure
  max_attempts: 3
  delay: 15s
  backoff: exponential
  max_delay: 5m
  reset_after: 10m

routing:
  hostname: chat.example.local
  tls: true

env:
  PORT: "8090"
  OLLAMA_HOST: http://127.0.0.1:11434

secrets:
  DATABASE_URL:
    keychain: aurelia/chat/database-url

env_file:
  - config/chat.env

volumes:
  /data: /tmp/testdata
  /config: /tmp/testconfig:ro

dependencies:
  after: [postgres, auth]
  requires: [postgres]
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	spec, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if spec.Service.Name != "chat" {
		t.Errorf("expected name 'chat', got %q", spec.Service.Name)
	}
	if spec.Service.Type != "container" {
		t.Errorf("expected type 'container', got %q", spec.Service.Type)
	}
	if spec.Service.Image != "chat:prod" {
		t.Errorf("expected image 'chat:prod', got %q", spec.Service.Image)
	}
	if spec.Service.NetworkMode != "host" {
		t.Errorf("expected network_mode 'host', got %q", spec.Service.NetworkMode)
	}
	if spec.Network.Port != 8090 {
		t.Errorf("expected port 8090, got %d", spec.Network.Port)
	}
	if spec.Health.Type != "http" {
		t.Errorf("expected health type 'http', got %q", spec.Health.Type)
	}
	if spec.Health.Path != "/health" {
		t.Errorf("expected health path '/health', got %q", spec.Health.Path)
	}
	if spec.Health.Interval.Duration != 10*time.Second {
		t.Errorf("expected interval 10s, got %v", spec.Health.Interval.Duration)
	}
	if spec.Health.Timeout.Duration != 2*time.Second {
		t.Errorf("expected timeout 2s, got %v", spec.Health.Timeout.Duration)
	}
	if spec.Health.GracePeriod.Duration != 5*time.Second {
		t.Errorf("expected grace_period 5s, got %v", spec.Health.GracePeriod.Duration)
	}
	if spec.Health.UnhealthyThreshold != 3 {
		t.Errorf("expected unhealthy_threshold 3, got %d", spec.Health.UnhealthyThreshold)
	}
	if spec.Restart.Policy != "on-failure" {
		t.Errorf("expected restart policy 'on-failure', got %q", spec.Restart.Policy)
	}
	if spec.Restart.MaxAttempts != 3 {
		t.Errorf("expected max_attempts 3, got %d", spec.Restart.MaxAttempts)
	}
	if spec.Restart.Delay.Duration != 15*time.Second {
		t.Errorf("expected delay 15s, got %v", spec.Restart.Delay.Duration)
	}
	if spec.Restart.Backoff != "exponential" {
		t.Errorf("expected backoff 'exponential', got %q", spec.Restart.Backoff)
	}
	if spec.Env["PORT"] != "8090" {
		t.Errorf("expected env PORT='8090', got %q", spec.Env["PORT"])
	}
	if spec.Secrets["DATABASE_URL"].Keychain != "aurelia/chat/database-url" {
		t.Errorf("expected secret keychain ref, got %q", spec.Secrets["DATABASE_URL"].Keychain)
	}
	if len(spec.EnvFile) != 1 || spec.EnvFile[0] != "config/chat.env" {
		t.Errorf("expected env_file [config/chat.env], got %v", spec.EnvFile)
	}
	if spec.Volumes["/data"] != "/tmp/testdata" {
		t.Errorf("expected volume /data mapping, got %q", spec.Volumes["/data"])
	}
	if len(spec.Dependencies.After) != 2 {
		t.Errorf("expected 2 after dependencies, got %d", len(spec.Dependencies.After))
	}
	if len(spec.Dependencies.Requires) != 1 || spec.Dependencies.Requires[0] != "postgres" {
		t.Errorf("expected requires [postgres], got %v", spec.Dependencies.Requires)
	}
	if spec.Routing == nil {
		t.Fatal("expected routing block")
	}
	if spec.Routing.Hostname != "chat.example.local" {
		t.Errorf("expected hostname 'chat.example.local', got %q", spec.Routing.Hostname)
	}
	if !spec.Routing.TLS {
		t.Error("expected tls true")
	}
}

func TestLoadValidNativeSpec(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ollama.yaml")
	data := `
service:
  name: ollama
  type: native
  command: /usr/local/bin/ollama serve

network:
  port: 11434

health:
  type: http
  path: /
  interval: 15s
  timeout: 3s
  grace_period: 10s

restart:
  policy: always
  delay: 5s

env:
  OLLAMA_HOST: "0.0.0.0"
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	spec, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if spec.Service.Name != "ollama" {
		t.Errorf("expected name 'ollama', got %q", spec.Service.Name)
	}
	if spec.Service.Type != "native" {
		t.Errorf("expected type 'native', got %q", spec.Service.Type)
	}
	if spec.Service.Command != "/usr/local/bin/ollama serve" {
		t.Errorf("expected command, got %q", spec.Service.Command)
	}
	if spec.Restart.Policy != "always" {
		t.Errorf("expected restart policy 'always', got %q", spec.Restart.Policy)
	}
}

func TestValidateServiceSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec *ServiceSpec
	}{
		{
			name: "missing service name",
			spec: &ServiceSpec{
				Service: Service{Type: "native", Command: "echo"},
			},
		},
		{
			name: "native without command",
			spec: &ServiceSpec{
				Service: Service{Name: "test", Type: "native"},
			},
		},
		{
			name: "container without image",
			spec: &ServiceSpec{
				Service: Service{Name: "test", Type: "container"},
			},
		},
		{
			name: "invalid service type",
			spec: &ServiceSpec{
				Service: Service{Name: "test", Type: "invalid"},
			},
		},
		{
			name: "image on native service",
			spec: &ServiceSpec{
				Service: Service{Name: "test", Type: "native", Command: "echo", Image: "foo:bar"},
			},
		},
		{
			name: "command on container service",
			spec: &ServiceSpec{
				Service: Service{Name: "test", Type: "container", Image: "foo:bar", Command: "echo"},
			},
		},
		{
			name: "invalid service name with slashes",
			spec: &ServiceSpec{
				Service: Service{Name: "my/service", Type: "native", Command: "echo"},
			},
		},
		{
			name: "invalid service name with dotdot",
			spec: &ServiceSpec{
				Service: Service{Name: "..badname", Type: "native", Command: "echo"},
			},
		},
		{
			name: "invalid hostname with backtick",
			spec: &ServiceSpec{
				Service: Service{Name: "test", Type: "native", Command: "echo"},
				Network: &Network{Port: 8080},
				Routing: &Routing{Hostname: "bad`host.local"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.spec.Validate(); err == nil {
				t.Errorf("expected validation error for %s", tt.name)
			}
		})
	}
}

func TestValidateServiceName(t *testing.T) {
	t.Parallel()

	t.Run("valid service name", func(t *testing.T) {
		t.Parallel()
		spec := &ServiceSpec{
			Service: Service{Name: "my-service_1.0", Type: "native", Command: "echo"},
		}
		if err := spec.Validate(); err != nil {
			t.Errorf("expected valid service name to pass, got: %v", err)
		}
	})

	t.Run("invalid service name with slashes", func(t *testing.T) {
		t.Parallel()
		spec := &ServiceSpec{
			Service: Service{Name: "my/service", Type: "native", Command: "echo"},
		}
		if err := spec.Validate(); err == nil {
			t.Error("expected validation error for service name with slashes")
		}
	})

	t.Run("invalid service name with dotdot", func(t *testing.T) {
		t.Parallel()
		spec := &ServiceSpec{
			Service: Service{Name: "..badname", Type: "native", Command: "echo"},
		}
		if err := spec.Validate(); err == nil {
			t.Error("expected validation error for service name starting with ..")
		}
	})
}

func TestValidateRoutingHostname(t *testing.T) {
	t.Parallel()

	t.Run("valid hostname", func(t *testing.T) {
		t.Parallel()
		spec := &ServiceSpec{
			Service: Service{Name: "test", Type: "native", Command: "echo"},
			Network: &Network{Port: 8080},
			Routing: &Routing{Hostname: "my-service.example.local"},
		}
		if err := spec.Validate(); err != nil {
			t.Errorf("expected valid hostname to pass, got: %v", err)
		}
	})

	t.Run("invalid hostname with backtick", func(t *testing.T) {
		t.Parallel()
		spec := &ServiceSpec{
			Service: Service{Name: "test", Type: "native", Command: "echo"},
			Network: &Network{Port: 8080},
			Routing: &Routing{Hostname: "bad`host.local"},
		}
		if err := spec.Validate(); err == nil {
			t.Error("expected validation error for hostname with backtick")
		}
	})
}

func TestValidateHealthCheckTypes(t *testing.T) {
	t.Parallel()
	base := ServiceSpec{
		Service: Service{Name: "test", Type: "native", Command: "echo"},
	}

	// http without path
	s := base
	s.Health = &HealthCheck{Type: "http", Interval: Duration{10 * time.Second}, Timeout: Duration{2 * time.Second}}
	if err := s.Validate(); err == nil {
		t.Error("expected error for http health check without path")
	}

	// exec without command
	s = base
	s.Health = &HealthCheck{Type: "exec", Interval: Duration{10 * time.Second}, Timeout: Duration{2 * time.Second}}
	if err := s.Validate(); err == nil {
		t.Error("expected error for exec health check without command")
	}

	// invalid type
	s = base
	s.Health = &HealthCheck{Type: "grpc", Interval: Duration{10 * time.Second}, Timeout: Duration{2 * time.Second}}
	if err := s.Validate(); err == nil {
		t.Error("expected error for invalid health check type")
	}
}

func TestValidateRestartPolicy(t *testing.T) {
	t.Parallel()
	base := ServiceSpec{
		Service: Service{Name: "test", Type: "native", Command: "echo"},
	}

	s := base
	s.Restart = &RestartPolicy{Policy: "invalid"}
	if err := s.Validate(); err == nil {
		t.Error("expected error for invalid restart policy")
	}

	s = base
	s.Restart = &RestartPolicy{Policy: "always", Backoff: "invalid"}
	if err := s.Validate(); err == nil {
		t.Error("expected error for invalid backoff type")
	}
}

func TestValidateRoutingRequiresHostname(t *testing.T) {
	t.Parallel()
	spec := &ServiceSpec{
		Service: Service{Name: "test", Type: "native", Command: "echo"},
		Network: &Network{Port: 8080},
		Routing: &Routing{TLS: true},
	}
	if err := spec.Validate(); err == nil {
		t.Error("expected error for routing without hostname")
	}
}

func TestValidateRoutingRequiresPort(t *testing.T) {
	t.Parallel()
	spec := &ServiceSpec{
		Service: Service{Name: "test", Type: "native", Command: "echo"},
		Routing: &Routing{Hostname: "test.example.local"},
	}
	if err := spec.Validate(); err == nil {
		t.Error("expected error for routing without port")
	}
}

func TestValidateRoutingAcceptsHealthPort(t *testing.T) {
	t.Parallel()
	spec := &ServiceSpec{
		Service: Service{Name: "test", Type: "native", Command: "echo"},
		Health:  &HealthCheck{Type: "http", Path: "/health", Port: 8080, Interval: Duration{10 * time.Second}, Timeout: Duration{2 * time.Second}},
		Routing: &Routing{Hostname: "test.example.local"},
	}
	if err := spec.Validate(); err != nil {
		t.Errorf("routing with health port should be valid: %v", err)
	}
}

func TestValidateRoutingWithTLSOptions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "signal.yaml")
	data := `
service:
  name: signal-api
  type: container
  image: signal:latest

network:
  port: 8093

routing:
  hostname: signal-api.example.local
  tls: true
  tls_options: mtls
`
	os.WriteFile(path, []byte(data), 0644)

	spec, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Routing.TLSOptions != "mtls" {
		t.Errorf("expected tls_options 'mtls', got %q", spec.Routing.TLSOptions)
	}
}

func TestValidateRequiresMustBeInAfter(t *testing.T) {
	t.Parallel()
	spec := &ServiceSpec{
		Service: Service{Name: "test", Type: "native", Command: "echo"},
		Dependencies: &Dependencies{
			After:    []string{"postgres"},
			Requires: []string{"redis"}, // not in after
		},
	}
	if err := spec.Validate(); err == nil {
		t.Error("expected error when requires has entry not in after")
	}
}

func TestNeedsDynamicPort(t *testing.T) {
	t.Parallel()
	// No network block
	s := &ServiceSpec{Service: Service{Name: "test", Type: "native", Command: "echo"}}
	if s.NeedsDynamicPort() {
		t.Error("expected false when no network block")
	}

	// Static port
	s.Network = &Network{Port: 8080}
	if s.NeedsDynamicPort() {
		t.Error("expected false for static port")
	}

	// Dynamic port (port 0)
	s.Network = &Network{Port: 0}
	if !s.NeedsDynamicPort() {
		t.Error("expected true for port 0")
	}
}

func TestValidateRoutingAllowsDynamicPort(t *testing.T) {
	t.Parallel()
	s := &ServiceSpec{
		Service: Service{Name: "test", Type: "native", Command: "echo"},
		Network: &Network{Port: 0},
		Routing: &Routing{Hostname: "test.example.local"},
	}
	if err := s.Validate(); err != nil {
		t.Errorf("routing with dynamic port (0) should be valid: %v", err)
	}
}

func TestLoadDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	chat := `
service:
  name: chat
  type: container
  image: chat:prod

health:
  type: http
  path: /health
  interval: 10s
  timeout: 2s
`
	ollama := `
service:
  name: ollama
  type: native
  command: /usr/local/bin/ollama serve

health:
  type: http
  path: /
  interval: 15s
  timeout: 3s
`
	os.WriteFile(filepath.Join(dir, "chat.yaml"), []byte(chat), 0644)
	os.WriteFile(filepath.Join(dir, "ollama.yml"), []byte(ollama), 0644)

	specs, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}

	names := map[string]bool{}
	for _, s := range specs {
		names[s.Service.Name] = true
	}
	if !names["chat"] || !names["ollama"] {
		t.Errorf("expected chat and ollama, got %v", names)
	}
}

func TestSecretRefWithRotation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	data := `
service:
  name: test
  type: native
  command: echo

secrets:
  API_KEY:
    keychain: aurelia/test/api-key
    rotate_every: 30d
    rotate_command: scripts/rotate.sh
`
	os.WriteFile(path, []byte(data), 0644)

	spec, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	secret := spec.Secrets["API_KEY"]
	if secret.Keychain != "aurelia/test/api-key" {
		t.Errorf("expected keychain ref, got %q", secret.Keychain)
	}
	if secret.RotateEvery != "30d" {
		t.Errorf("expected rotate_every '30d', got %q", secret.RotateEvery)
	}
	if secret.RotateCommand != "scripts/rotate.sh" {
		t.Errorf("expected rotate_command, got %q", secret.RotateCommand)
	}
}
