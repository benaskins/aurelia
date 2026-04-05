package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/benaskins/aurelia/internal/config"
	"github.com/benaskins/aurelia/internal/diagnose"
	talk "github.com/benaskins/axon-talk"
	"github.com/benaskins/axon-talk/anthropic"
	"github.com/benaskins/axon-talk/openai"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var diagnoseCmd = &cobra.Command{
	Use:   "diagnose [service]",
	Short: "LLM-powered diagnosis of managed services",
	Long:  "Interactive diagnostic conversation — aurelia reasons about its managed services using LLM tool calls.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cleanup, err := diagnose.SetupLogging()
		if err != nil {
			return fmt.Errorf("setting up logging: %w", err)
		}
		defer cleanup()

		cfg, err := config.Load(config.DefaultPath())
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if cfg.Diagnose == nil {
			return fmt.Errorf("diagnose not configured — add a diagnose section to ~/.aurelia/config.yaml")
		}

		apiKey, err := resolveAPIKey(cfg.Diagnose.APIKeySecret)
		if err != nil {
			return fmt.Errorf("resolving API key: %w", err)
		}

		llm, err := newLLMClient(cfg.Diagnose.Provider, cfg.Diagnose.BaseURL, apiKey)
		if err != nil {
			return err
		}

		apiClient, err := newDiagnoseAPIClient()
		if err != nil {
			return err
		}

		var service string
		if len(args) > 0 {
			service = args[0]
		}

		// Create the TUI program first, then wire confirmation through it
		var p *tea.Program

		confirm := diagnose.TUIConfirm(func(msg tea.Msg) {
			if p != nil {
				p.Send(msg)
			}
		})

		engine := diagnose.NewEngineWithActions(llm, cfg.Diagnose.Model, apiClient, confirm)
		model := diagnose.NewTUIModel(engine, service)

		p = tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("TUI error: %w", err)
		}
		return nil
	},
}

func resolveAPIKey(secretName string) (string, error) {
	if secretName == "" {
		return "", fmt.Errorf("api_key_secret not configured")
	}
	store, err := newSecretStore("diagnose")
	if err == nil {
		if val, err := store.Get(secretName); err == nil {
			return val, nil
		}
	}
	// Fall back to environment variable (uppercase, hyphens to underscores)
	envKey := strings.ToUpper(strings.ReplaceAll(secretName, "-", "_"))
	if val := os.Getenv(envKey); val != "" {
		return val, nil
	}
	return "", fmt.Errorf("secret %q not found in store and %s not set in environment", secretName, envKey)
}

func newLLMClient(provider, baseURL, apiKey string) (talk.LLMClient, error) {
	switch provider {
	case "anthropic":
		return anthropic.NewClient("https://api.anthropic.com", apiKey), nil
	case "openai":
		if baseURL == "" {
			baseURL = "https://api.openai.com"
		}
		return openai.NewClient(baseURL, apiKey), nil
	default:
		return nil, fmt.Errorf("unsupported diagnose provider %q (supported: anthropic, openai)", provider)
	}
}

func newDiagnoseAPIClient() (diagnose.APIClient, error) {
	socketPath, err := defaultSocketPath()
	if err != nil {
		return nil, err
	}
	return &socketAPIClient{
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
		},
	}, nil
}

// socketAPIClient implements diagnose.APIClient over a Unix socket.
type socketAPIClient struct {
	client *http.Client
}

func (c *socketAPIClient) Get(path string) (*http.Response, error) {
	return c.client.Get("http://aurelia" + path)
}

func (c *socketAPIClient) Post(path string) (*http.Response, error) {
	return c.client.Post("http://aurelia"+path, "application/json", nil)
}

func (c *socketAPIClient) Delete(path string) (*http.Response, error) {
	req, err := http.NewRequest("DELETE", "http://aurelia"+path, nil)
	if err != nil {
		return nil, err
	}
	return c.client.Do(req)
}

func init() {
	rootCmd.AddCommand(diagnoseCmd)
}
