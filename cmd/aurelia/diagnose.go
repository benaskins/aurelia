package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	talk "github.com/benaskins/axon-talk"
	"github.com/benaskins/axon-talk/anthropic"
	"github.com/benaskins/axon-talk/ollama"
	"github.com/benaskins/aurelia/internal/config"
	"github.com/benaskins/aurelia/internal/diagnose"
	"github.com/spf13/cobra"
)

var diagnoseCmd = &cobra.Command{
	Use:   "diagnose [service]",
	Short: "LLM-powered diagnosis of managed services",
	Long:  "Uses an LLM to analyze service state, logs, and health — gathering evidence via aurelia's API before diagnosing.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
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

		llm, err := newLLMClient(cfg.Diagnose.Provider, apiKey)
		if err != nil {
			return err
		}

		apiClient, err := newDiagnoseAPIClient()
		if err != nil {
			return err
		}

		engine := diagnose.NewEngine(llm, cfg.Diagnose.Model, apiClient)

		var service string
		if len(args) > 0 {
			service = args[0]
		}

		_, err = engine.Diagnose(context.Background(), service, func(token string) {
			fmt.Print(token)
		})
		fmt.Println()
		return err
	},
}

func resolveAPIKey(secretName string) (string, error) {
	if secretName == "" {
		return "", fmt.Errorf("api_key_secret not configured")
	}
	store, err := newSecretStore("diagnose")
	if err != nil {
		return "", err
	}
	return store.Get(secretName)
}

func newLLMClient(provider, apiKey string) (talk.LLMClient, error) {
	switch provider {
	case "anthropic":
		return anthropic.NewClient("https://api.anthropic.com", apiKey), nil
	case "ollama":
		return ollama.NewClientFromEnvironment()
	default:
		return nil, fmt.Errorf("unsupported diagnose provider %q (supported: anthropic, ollama)", provider)
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

func init() {
	rootCmd.AddCommand(diagnoseCmd)
}
