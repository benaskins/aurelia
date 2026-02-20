package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/benaskins/aurelia/internal/daemon"
	"github.com/benaskins/aurelia/internal/gpu"
	"github.com/spf13/cobra"
)

func apiClient() *http.Client {
	socketPath := defaultSocketPath()
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}
}

func apiGet(path string, v any) error {
	resp, err := apiClient().Get("http://aurelia" + path)
	if err != nil {
		return fmt.Errorf("connecting to daemon: %w (is aurelia daemon running?)", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("API error (%d): %s", resp.StatusCode, body)
	}

	return json.NewDecoder(resp.Body).Decode(v)
}

func apiPost(path string) (map[string]any, error) {
	resp, err := apiClient().Post("http://aurelia"+path, "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w (is aurelia daemon running?)", err)
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if resp.StatusCode >= 400 {
		if msg, ok := result["error"]; ok {
			return nil, fmt.Errorf("%v", msg)
		}
		return nil, fmt.Errorf("API error (%d)", resp.StatusCode)
	}

	return result, nil
}

// status command
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show service status",
	RunE: func(cmd *cobra.Command, args []string) error {
		var states []daemon.ServiceState
		if err := apiGet("/v1/services", &states); err != nil {
			return err
		}

		if len(states) == 0 {
			fmt.Println("No services")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "SERVICE\tTYPE\tSTATE\tHEALTH\tPID\tUPTIME\tRESTARTS")
		for _, s := range states {
			pid := "-"
			if s.PID > 0 {
				pid = fmt.Sprintf("%d", s.PID)
			}
			uptime := "-"
			if s.Uptime != "" {
				uptime = s.Uptime
			}
			health := string(s.Health)
			if health == "" {
				health = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n",
				s.Name, s.Type, s.State, health, pid, uptime, s.RestartCount)
		}
		w.Flush()

		// GPU summary line
		gpuInfo := gpu.QueryNow()
		if gpuInfo.Name != "" {
			fmt.Printf("\nGPU: %s | VRAM: %.1f/%.1f GB | Thermal: %s\n",
				gpuInfo.Name, gpuInfo.AllocatedGB(), gpuInfo.RecommendedMaxGB(), gpuInfo.ThermalState)
		}

		return nil
	},
}

// up command
var upCmd = &cobra.Command{
	Use:   "up [service...]",
	Short: "Start services",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			// Start all — reload picks up everything
			result, err := apiPost("/v1/reload")
			if err != nil {
				return err
			}
			fmt.Printf("Services loaded: %v\n", result)
			return nil
		}

		for _, name := range args {
			result, err := apiPost(fmt.Sprintf("/v1/services/%s/start", name))
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
				continue
			}
			fmt.Printf("%s: %v\n", name, result["status"])
		}
		return nil
	},
}

// down command
var downCmd = &cobra.Command{
	Use:   "down [service...]",
	Short: "Stop services",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			// Stop all
			var states []daemon.ServiceState
			if err := apiGet("/v1/services", &states); err != nil {
				return err
			}
			for _, s := range states {
				args = append(args, s.Name)
			}
		}

		for _, name := range args {
			result, err := apiPost(fmt.Sprintf("/v1/services/%s/stop", name))
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
				continue
			}
			fmt.Printf("%s: %v\n", name, result["status"])
		}
		return nil
	},
}

// restart command
var restartCmd = &cobra.Command{
	Use:   "restart <service>",
	Short: "Restart a service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiPost(fmt.Sprintf("/v1/services/%s/restart", args[0]))
		if err != nil {
			return err
		}
		fmt.Printf("%s: %v\n", args[0], result["status"])
		return nil
	},
}

// reload command
var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Reload service specs",
	Long:  "Re-read spec files and reconcile: start new services, stop removed ones.",
	RunE: func(cmd *cobra.Command, args []string) error {
		var result map[string]any
		resp, err := apiClient().Post("http://aurelia/v1/reload", "application/json", nil)
		if err != nil {
			return fmt.Errorf("connecting to daemon: %w", err)
		}
		defer resp.Body.Close()
		json.NewDecoder(resp.Body).Decode(&result)

		if added, ok := result["added"]; ok {
			fmt.Printf("Added: %v\n", added)
		}
		if removed, ok := result["removed"]; ok {
			fmt.Printf("Removed: %v\n", removed)
		}
		if result["added"] == nil && result["removed"] == nil {
			fmt.Println("No changes")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(reloadCmd)
}
