package spec

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadValidContainerSpec(t *testing.T) {
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

env:
  PORT: "8090"
  OLLAMA_HOST: http://127.0.0.1:11434

secrets:
  DATABASE_URL:
    keychain: aurelia/chat/database-url

env_file:
  - config/chat.env

volumes:
  /data: /Users/ben/data
  /config: /Users/ben/config:ro

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
	if spec.Volumes["/data"] != "/Users/ben/data" {
		t.Errorf("expected volume /data mapping, got %q", spec.Volumes["/data"])
	}
	if len(spec.Dependencies.After) != 2 {
		t.Errorf("expected 2 after dependencies, got %d", len(spec.Dependencies.After))
	}
	if len(spec.Dependencies.Requires) != 1 || spec.Dependencies.Requires[0] != "postgres" {
		t.Errorf("expected requires [postgres], got %v", spec.Dependencies.Requires)
	}
}

func TestLoadValidNativeSpec(t *testing.T) {
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

func TestValidateRequiresServiceName(t *testing.T) {
	spec := &ServiceSpec{
		Service: Service{Type: "native", Command: "echo"},
	}
	if err := spec.Validate(); err == nil {
		t.Error("expected error for missing service name")
	}
}

func TestValidateNativeRequiresCommand(t *testing.T) {
	spec := &ServiceSpec{
		Service: Service{Name: "test", Type: "native"},
	}
	if err := spec.Validate(); err == nil {
		t.Error("expected error for native service without command")
	}
}

func TestValidateContainerRequiresImage(t *testing.T) {
	spec := &ServiceSpec{
		Service: Service{Name: "test", Type: "container"},
	}
	if err := spec.Validate(); err == nil {
		t.Error("expected error for container service without image")
	}
}

func TestValidateRejectsInvalidType(t *testing.T) {
	spec := &ServiceSpec{
		Service: Service{Name: "test", Type: "invalid"},
	}
	if err := spec.Validate(); err == nil {
		t.Error("expected error for invalid service type")
	}
}

func TestValidateRejectsImageOnNative(t *testing.T) {
	spec := &ServiceSpec{
		Service: Service{Name: "test", Type: "native", Command: "echo", Image: "foo:bar"},
	}
	if err := spec.Validate(); err == nil {
		t.Error("expected error for image on native service")
	}
}

func TestValidateRejectsCommandOnContainer(t *testing.T) {
	spec := &ServiceSpec{
		Service: Service{Name: "test", Type: "container", Image: "foo:bar", Command: "echo"},
	}
	if err := spec.Validate(); err == nil {
		t.Error("expected error for command on container service")
	}
}

func TestValidateHealthCheckTypes(t *testing.T) {
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

func TestValidateRequiresMustBeInAfter(t *testing.T) {
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

func TestLoadDir(t *testing.T) {
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
