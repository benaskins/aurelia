package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// ServiceSpec is the top-level structure for a service definition.
type ServiceSpec struct {
	Service      Service              `yaml:"service"`
	Network      *Network             `yaml:"network,omitempty"`
	Routing      *Routing             `yaml:"routing,omitempty"`
	Health       *HealthCheck         `yaml:"health,omitempty"`
	Restart      *RestartPolicy       `yaml:"restart,omitempty"`
	Env          map[string]string    `yaml:"env,omitempty"`
	Secrets      map[string]SecretRef `yaml:"secrets,omitempty"`
	EnvFile      []string             `yaml:"env_file,omitempty"`
	Volumes      map[string]string    `yaml:"volumes,omitempty"`
	Dependencies *Dependencies        `yaml:"dependencies,omitempty"`
}

type Service struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`                  // "native" | "container"
	Command     string `yaml:"command,omitempty"`      // native only
	WorkingDir  string `yaml:"working_dir,omitempty"`  // native only
	Image       string `yaml:"image,omitempty"`        // container only
	NetworkMode string `yaml:"network_mode,omitempty"` // container only, default "bridge"
}

type Network struct {
	Port int `yaml:"port"`
}

type HealthCheck struct {
	Type               string   `yaml:"type"` // "http" | "tcp" | "exec"
	Path               string   `yaml:"path,omitempty"`
	Port               int      `yaml:"port,omitempty"`
	Command            string   `yaml:"command,omitempty"` // exec only
	Interval           Duration `yaml:"interval"`
	Timeout            Duration `yaml:"timeout"`
	GracePeriod        Duration `yaml:"grace_period,omitempty"`
	UnhealthyThreshold int      `yaml:"unhealthy_threshold,omitempty"`
}

type RestartPolicy struct {
	Policy      string   `yaml:"policy"` // "always" | "on-failure" | "never"
	MaxAttempts int      `yaml:"max_attempts,omitempty"`
	Delay       Duration `yaml:"delay,omitempty"`
	Backoff     string   `yaml:"backoff,omitempty"` // "fixed" | "exponential"
	MaxDelay    Duration `yaml:"max_delay,omitempty"`
	ResetAfter  Duration `yaml:"reset_after,omitempty"`
}

type SecretRef struct {
	Keychain      string `yaml:"keychain"`
	RotateEvery   string `yaml:"rotate_every,omitempty"`
	RotateCommand string `yaml:"rotate_command,omitempty"`
}

type Routing struct {
	Hostname   string `yaml:"hostname"`
	TLS        bool   `yaml:"tls,omitempty"`
	TLSOptions string `yaml:"tls_options,omitempty"` // e.g. "mtls" for mTLS enforcement
}

type Dependencies struct {
	After    []string `yaml:"after,omitempty"`
	Requires []string `yaml:"requires,omitempty"`
}

// Duration wraps time.Duration for YAML unmarshaling from strings like "10s", "5m".
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = parsed
	return nil
}

func (d Duration) MarshalYAML() (interface{}, error) {
	return d.Duration.String(), nil
}

// Load reads and parses a service spec from a YAML file.
func Load(path string) (*ServiceSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading spec %s: %w", path, err)
	}

	var spec ServiceSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parsing spec %s: %w", path, err)
	}

	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("validating spec %s: %w", path, err)
	}

	return &spec, nil
}

// LoadDir reads all YAML service specs from a directory.
func LoadDir(dir string) ([]*ServiceSpec, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("listing specs in %s: %w", dir, err)
	}

	// Also match .yml
	ymlEntries, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	if err != nil {
		return nil, fmt.Errorf("listing specs in %s: %w", dir, err)
	}
	entries = append(entries, ymlEntries...)

	var specs []*ServiceSpec
	for _, path := range entries {
		spec, err := Load(path)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}

	return specs, nil
}

// Validate checks that a service spec is well-formed.
func (s *ServiceSpec) Validate() error {
	if s.Service.Name == "" {
		return fmt.Errorf("service.name is required")
	}

	switch s.Service.Type {
	case "native":
		if s.Service.Command == "" {
			return fmt.Errorf("service.command is required for native services")
		}
		if s.Service.Image != "" {
			return fmt.Errorf("service.image is not valid for native services")
		}
	case "container":
		if s.Service.Image == "" {
			return fmt.Errorf("service.image is required for container services")
		}
		if s.Service.Command != "" {
			return fmt.Errorf("service.command is not valid for container services")
		}
	default:
		return fmt.Errorf("service.type must be \"native\" or \"container\", got %q", s.Service.Type)
	}

	if h := s.Health; h != nil {
		switch h.Type {
		case "http":
			if h.Path == "" {
				return fmt.Errorf("health.path is required for http health checks")
			}
		case "tcp":
			// port is sufficient
		case "exec":
			if h.Command == "" {
				return fmt.Errorf("health.command is required for exec health checks")
			}
		default:
			return fmt.Errorf("health.type must be \"http\", \"tcp\", or \"exec\", got %q", h.Type)
		}

		if h.Interval.Duration <= 0 {
			return fmt.Errorf("health.interval must be positive")
		}
		if h.Timeout.Duration <= 0 {
			return fmt.Errorf("health.timeout must be positive")
		}
	}

	if r := s.Restart; r != nil {
		switch r.Policy {
		case "always", "on-failure", "never":
			// ok
		default:
			return fmt.Errorf("restart.policy must be \"always\", \"on-failure\", or \"never\", got %q", r.Policy)
		}

		if r.Backoff != "" {
			switch r.Backoff {
			case "fixed", "exponential":
				// ok
			default:
				return fmt.Errorf("restart.backoff must be \"fixed\" or \"exponential\", got %q", r.Backoff)
			}
		}
	}

	if r := s.Routing; r != nil {
		if r.Hostname == "" {
			return fmt.Errorf("routing.hostname is required")
		}
		// Routing requires a port (from network block or health block)
		port := 0
		if s.Network != nil {
			port = s.Network.Port
		}
		if port == 0 && s.Health != nil {
			port = s.Health.Port
		}
		if port == 0 {
			return fmt.Errorf("routing requires a network.port")
		}
	}

	if deps := s.Dependencies; deps != nil {
		for _, req := range deps.Requires {
			found := false
			for _, after := range deps.After {
				if after == req {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("dependency %q is in requires but not in after — required services must also be in the start order", req)
			}
		}
	}

	return nil
}
