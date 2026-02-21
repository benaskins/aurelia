package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/benaskins/aurelia/internal/daemon"
	"github.com/benaskins/aurelia/internal/driver"
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
		return fmt.Errorf("API error %d: %s", resp.StatusCode, body)
	}

	return json.NewDecoder(resp.Body).Decode(v)
}

func apiPost(path string) (map[string]any, error) {
	resp, err := apiClient().Post("http://aurelia"+path, "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w (is aurelia daemon running?)", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
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
		fmt.Fprintln(w, "SERVICE\tTYPE\tSTATE\tHEALTH\tPID\tPORT\tUPTIME\tRESTARTS")
		for _, s := range states {
			pid := "-"
			if s.PID > 0 {
				pid = fmt.Sprintf("%d", s.PID)
			}
			port := "-"
			if s.Port > 0 {
				port = fmt.Sprintf("%d", s.Port)
			}
			uptime := "-"
			if s.Uptime != "" {
				uptime = s.Uptime
			}
			health := string(s.Health)
			if health == "" {
				health = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\n",
				s.Name, s.Type, s.State, health, pid, port, uptime, s.RestartCount)
		}
		w.Flush()

		// Show details for failed services
		for _, s := range states {
			if s.State == driver.StateFailed {
				detail := fmt.Sprintf("\n%s: exit %d", s.Name, s.LastExitCode)
				if s.LastError != "" {
					detail += fmt.Sprintf(" — %s", s.LastError)
				}
				fmt.Println(detail)
			}
		}

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
	Use:     "up [service...]",
	Aliases: []string{"start"},
	Short:   "Start services",
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
	Use:     "down [service...]",
	Aliases: []string{"stop"},
	Short:   "Stop services",
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

// deploy command
var deployCmd = &cobra.Command{
	Use:   "deploy <service>",
	Short: "Zero-downtime deploy a service",
	Long:  "Performs a blue-green deploy: starts new instance, verifies health, switches routing, drains old.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		drain, _ := cmd.Flags().GetString("drain")
		path := fmt.Sprintf("/v1/services/%s/deploy", args[0])
		if drain != "" {
			path += "?drain=" + drain
		}
		client := apiClient()
		client.Timeout = 5 * time.Minute // deploy can take a while
		resp, err := client.Post("http://aurelia"+path, "application/json", nil)
		if err != nil {
			return fmt.Errorf("connecting to daemon: %w (is aurelia daemon running?)", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			return fmt.Errorf("deploy failed: %s", body)
		}

		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)
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
		result, err := apiPost("/v1/reload")
		if err != nil {
			return err
		}

		if added, ok := result["added"]; ok {
			fmt.Printf("Added: %v\n", added)
		}
		if removed, ok := result["removed"]; ok {
			fmt.Printf("Removed: %v\n", removed)
		}
		if restarted, ok := result["restarted"]; ok {
			fmt.Printf("Restarted: %v\n", restarted)
		}
		if result["added"] == nil && result["removed"] == nil && result["restarted"] == nil {
			fmt.Println("No changes")
		}
		return nil
	},
}

// logs command
var logsCmd = &cobra.Command{
	Use:   "logs <service>",
	Short: "Show recent log output for a service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		n, _ := cmd.Flags().GetInt("lines")
		var resp struct {
			Lines []string `json:"lines"`
		}
		if err := apiGet(fmt.Sprintf("/v1/services/%s/logs?n=%s", args[0], strconv.Itoa(n)), &resp); err != nil {
			return err
		}
		for _, line := range resp.Lines {
			fmt.Println(line)
		}
		return nil
	},
}

func init() {
	logsCmd.Flags().IntP("lines", "n", 50, "number of lines to show")
	deployCmd.Flags().String("drain", "5s", "drain period before stopping old instance")

	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(deployCmd)
	rootCmd.AddCommand(reloadCmd)
	rootCmd.AddCommand(logsCmd)
}
