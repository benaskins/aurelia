package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/benaskins/aurelia/internal/config"
	"github.com/benaskins/aurelia/internal/daemon"
	"github.com/benaskins/aurelia/internal/driver"
	"github.com/benaskins/aurelia/internal/gpu"
	"github.com/benaskins/aurelia/internal/node"
	"github.com/benaskins/aurelia/internal/spec"
	"github.com/spf13/cobra"
)

func apiClient() (*http.Client, error) {
	socketPath, err := defaultSocketPath()
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}, nil
}

// apiStreamClient returns an HTTP client suitable for streaming responses.
// Unlike apiClient, it has no request timeout — the caller controls lifetime
// via context cancellation.
func apiStreamClient() (*http.Client, error) {
	socketPath, err := defaultSocketPath()
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}, nil
}

func apiGet(path string, v any) error {
	client, err := apiClient()
	if err != nil {
		return err
	}
	resp, err := client.Get("http://aurelia" + path)
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
	client, err := apiClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.Post("http://aurelia"+path, "application/json", nil)
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

// resolveNodeClient returns a node.Client if --node is set, or nil for local.
func resolveNodeClient(cmd *cobra.Command) (*node.Client, error) {
	nodeName, _ := cmd.Flags().GetString("node")
	if nodeName == "" {
		return nil, nil
	}
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	return buildPeerClient(cfg, nodeName)
}

// status command
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show service status",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOut, _ := cmd.Flags().GetBool("json")

		// If --node is set, query that specific remote node directly
		remote, err := resolveNodeClient(cmd)
		if err != nil {
			return err
		}

		var states []daemon.ServiceState
		if remote != nil {
			raw, err := remote.Status()
			if err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &states); err != nil {
				return fmt.Errorf("decoding status: %w", err)
			}
			// Stamp node name on each state
			for i := range states {
				if states[i].Node == "" {
					states[i].Node = remote.Name
				}
			}
		} else {
			// Use cluster endpoint to aggregate all nodes
			var clusterResp struct {
				Services []daemon.ServiceState `json:"services"`
				Peers    map[string]string     `json:"peers"`
			}
			if err := apiGet("/v1/cluster/services", &clusterResp); err != nil {
				// Fall back to local-only if cluster endpoint not available
				if err := apiGet("/v1/services", &states); err != nil {
					return err
				}
			} else {
				states = clusterResp.Services
			}
		}

		if jsonOut {
			return printJSON(states)
		}

		if len(states) == 0 {
			fmt.Println("No services")
			return nil
		}

		// Determine if we should show the NODE column
		hasNodes := false
		for _, s := range states {
			if s.Node != "" {
				hasNodes = true
				break
			}
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if hasNodes {
			fmt.Fprintln(w, "NODE\tSERVICE\tTYPE\tSTATE\tHEALTH\tPID\tPORT\tUPTIME\tRESTARTS")
		} else {
			fmt.Fprintln(w, "SERVICE\tTYPE\tSTATE\tHEALTH\tPID\tPORT\tUPTIME\tRESTARTS")
		}
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
			if hasNodes {
				nodeName := s.Node
				if nodeName == "" {
					nodeName = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\n",
					nodeName, s.Name, s.Type, s.State, health, pid, port, uptime, s.RestartCount)
			} else {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\n",
					s.Name, s.Type, s.State, health, pid, port, uptime, s.RestartCount)
			}
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

		// Spec drift check (local only, skip for remote queries)
		if remote == nil {
			checkSpecDrift()
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
		jsonOut, _ := cmd.Flags().GetBool("json")
		remote, err := resolveNodeClient(cmd)
		if err != nil {
			return err
		}

		if len(args) == 0 {
			if remote != nil {
				return remote.ReloadService()
			}
			// Start all — reload picks up everything
			result, err := apiPost("/v1/reload")
			if err != nil {
				return err
			}
			if jsonOut {
				return printJSON(result)
			}
			fmt.Printf("Services loaded: %v\n", result)
			return nil
		}

		var results []map[string]any
		for _, name := range args {
			var opErr error
			if remote != nil {
				opErr = remote.StartService(name)
			} else {
				_, opErr = apiPost(fmt.Sprintf("/v1/services/%s/start", name))
			}
			if opErr != nil {
				if jsonOut {
					results = append(results, map[string]any{"service": name, "error": opErr.Error()})
				} else {
					fmt.Fprintf(os.Stderr, "%s: %v\n", name, opErr)
				}
				continue
			}
			if jsonOut {
				results = append(results, map[string]any{"service": name, "status": "starting"})
			} else {
				fmt.Printf("%s: starting\n", name)
			}
		}
		if jsonOut {
			return printJSON(results)
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
		jsonOut, _ := cmd.Flags().GetBool("json")
		remote, err := resolveNodeClient(cmd)
		if err != nil {
			return err
		}

		if len(args) == 0 && remote == nil {
			// Stop all local
			var states []daemon.ServiceState
			if err := apiGet("/v1/services", &states); err != nil {
				return err
			}
			for _, s := range states {
				args = append(args, s.Name)
			}
		}

		var results []map[string]any
		for _, name := range args {
			var opErr error
			if remote != nil {
				opErr = remote.StopService(name)
			} else {
				_, opErr = apiPost(fmt.Sprintf("/v1/services/%s/stop", name))
			}
			if opErr != nil {
				if jsonOut {
					results = append(results, map[string]any{"service": name, "error": opErr.Error()})
				} else {
					fmt.Fprintf(os.Stderr, "%s: %v\n", name, opErr)
				}
				continue
			}
			if jsonOut {
				results = append(results, map[string]any{"service": name, "status": "stopping"})
			} else {
				fmt.Printf("%s: stopping\n", name)
			}
		}
		if jsonOut {
			return printJSON(results)
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
		jsonOut, _ := cmd.Flags().GetBool("json")
		remote, err := resolveNodeClient(cmd)
		if err != nil {
			return err
		}

		if remote != nil {
			if err := remote.RestartService(args[0]); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(map[string]string{"status": "restarting"})
			}
			fmt.Printf("%s: restarting\n", args[0])
			return nil
		}

		result, err := apiPost(fmt.Sprintf("/v1/services/%s/restart", args[0]))
		if err != nil {
			return err
		}
		if jsonOut {
			return printJSON(result)
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
		jsonOut, _ := cmd.Flags().GetBool("json")
		remote, err := resolveNodeClient(cmd)
		if err != nil {
			return err
		}

		if remote != nil {
			if err := remote.DeployService(args[0]); err != nil {
				return err
			}
			if jsonOut {
				return printJSON(map[string]string{"status": "deployed"})
			}
			fmt.Printf("%s: deployed\n", args[0])
			return nil
		}

		drain, _ := cmd.Flags().GetString("drain")
		path := fmt.Sprintf("/v1/services/%s/deploy", args[0])
		if drain != "" {
			path += "?drain=" + drain
		}
		client, err := apiClient()
		if err != nil {
			return err
		}
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

		if jsonOut {
			return printJSON(result)
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
		jsonOut, _ := cmd.Flags().GetBool("json")

		result, err := apiPost("/v1/reload")
		if err != nil {
			return err
		}

		if jsonOut {
			return printJSON(result)
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
var shipCmd = &cobra.Command{
	Use:   "ship <service>",
	Short: "Fetch, build, deploy, and notify for a service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOut, _ := cmd.Flags().GetBool("json")
		remote, err := resolveNodeClient(cmd)
		if err != nil {
			return err
		}

		var result daemon.ShipResult
		if remote != nil {
			raw, err := remote.Ship(args[0])
			if err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				return fmt.Errorf("decoding ship result: %w", err)
			}
		} else {
			r, err := apiShip(args[0])
			if err != nil {
				return err
			}
			result = *r
		}

		if jsonOut {
			return printJSON(result)
		}

		printShipResult(result)
		if !result.Success {
			return fmt.Errorf("ship failed")
		}
		return nil
	},
}

func apiShip(name string) (*daemon.ShipResult, error) {
	client, err := apiClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.Post("http://aurelia/v1/services/"+name+"/ship", "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w (is aurelia daemon running?)", err)
	}
	defer resp.Body.Close()

	var result daemon.ShipResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

func printShipResult(r daemon.ShipResult) {
	fmt.Printf("Shipping %s\n\n", r.Service)
	for _, step := range r.Steps {
		icon := "✓"
		if step.Status == "failed" {
			icon = "✗"
		} else if step.Status == "skipped" {
			icon = "–"
		}
		fmt.Printf("  %s %-8s %s\n", icon, step.Name, step.Duration)
		if step.Status == "failed" && step.Output != "" {
			for _, line := range strings.Split(step.Output, "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
	}
	fmt.Println()
	if r.Success {
		fmt.Printf("Shipped %s successfully\n", r.Service)
	} else {
		fmt.Printf("Ship failed for %s\n", r.Service)
	}
}

var inspectCmd = &cobra.Command{
	Use:   "inspect <service>",
	Short: "Show full resolved config and runtime state for a service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOut, _ := cmd.Flags().GetBool("json")
		remote, err := resolveNodeClient(cmd)
		if err != nil {
			return err
		}

		var si daemon.ServiceInspect
		if remote != nil {
			raw, err := remote.Inspect(args[0])
			if err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &si); err != nil {
				return fmt.Errorf("decoding inspect response: %w", err)
			}
		} else {
			if err := apiGet("/v1/services/"+args[0]+"/inspect", &si); err != nil {
				return err
			}
		}

		if jsonOut {
			return printJSON(si)
		}

		printInspect(si)
		return nil
	},
}

func printInspect(si daemon.ServiceInspect) {
	fmt.Printf("Service:      %s\n", si.Name)
	fmt.Printf("Type:         %s\n", si.Type)
	fmt.Printf("State:        %s\n", si.State)
	fmt.Printf("Health:       %s\n", si.Health)
	if si.PID != 0 {
		fmt.Printf("PID:          %d\n", si.PID)
	}
	if si.Port != 0 {
		fmt.Printf("Port:         %d\n", si.Port)
	}
	if si.Uptime != "" {
		fmt.Printf("Uptime:       %s\n", si.Uptime)
	}
	fmt.Printf("Restarts:     %d\n", si.RestartCount)

	if si.Command != "" {
		fmt.Printf("\nCommand:      %s\n", si.Command)
	}
	if si.Image != "" {
		fmt.Printf("\nImage:        %s\n", si.Image)
	}

	if si.Routing != nil {
		fmt.Println("\nRouting:")
		fmt.Printf("  Hostname:   %s\n", si.Routing.Hostname)
		fmt.Printf("  TLS:        %v\n", si.Routing.TLS)
	}

	if si.Dependencies != nil {
		fmt.Println("\nDependencies:")
		if len(si.Dependencies.After) > 0 {
			fmt.Printf("  After:      %s\n", strings.Join(si.Dependencies.After, ", "))
		}
		if len(si.Dependencies.Requires) > 0 {
			fmt.Printf("  Requires:   %s\n", strings.Join(si.Dependencies.Requires, ", "))
		}
	}

	if len(si.Env) > 0 {
		fmt.Println("\nEnv:")
		for k, v := range si.Env {
			fmt.Printf("  %-20s %s\n", k, v)
		}
	}

	if len(si.Secrets) > 0 {
		fmt.Println("\nSecrets:")
		for k, v := range si.Secrets {
			fmt.Printf("  %-20s %s\n", k, v)
		}
	}

	if si.HealthCheck != nil {
		fmt.Println("\nHealth Check:")
		fmt.Printf("  Type:       %s\n", si.HealthCheck.Type)
		if si.HealthCheck.Path != "" {
			fmt.Printf("  Path:       %s\n", si.HealthCheck.Path)
		}
		fmt.Printf("  Interval:   %s\n", si.HealthCheck.Interval.Duration)
		fmt.Printf("  Timeout:    %s\n", si.HealthCheck.Timeout.Duration)
	}

	if si.Restart != nil {
		fmt.Println("\nRestart:")
		fmt.Printf("  Policy:     %s\n", si.Restart.Policy)
		if si.Restart.MaxAttempts > 0 {
			fmt.Printf("  Max:        %d\n", si.Restart.MaxAttempts)
		}
		fmt.Printf("  Delay:      %s\n", si.Restart.Delay.Duration)
		if si.Restart.Backoff != "" {
			fmt.Printf("  Backoff:    %s\n", si.Restart.Backoff)
		}
	}

	if si.SpecHash != "" {
		fmt.Printf("\nSpec Hash:    %s\n", si.SpecHash)
	}
}

var logsCmd = &cobra.Command{
	Use:   "logs <service>",
	Short: "Show recent log output for a service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		follow, _ := cmd.Flags().GetBool("follow")
		jsonOut, _ := cmd.Flags().GetBool("json")
		n, _ := cmd.Flags().GetInt("lines")
		remote, err := resolveNodeClient(cmd)
		if err != nil {
			return err
		}

		if follow {
			if remote != nil {
				return fmt.Errorf("--follow is not supported with --node")
			}
			return logsFollow(args[0])
		}

		var lines []string
		if remote != nil {
			lines, err = remote.Logs(args[0], n)
			if err != nil {
				return err
			}
		} else {
			var resp struct {
				Lines []string `json:"lines"`
			}
			if err := apiGet(fmt.Sprintf("/v1/services/%s/logs?n=%s", args[0], strconv.Itoa(n)), &resp); err != nil {
				return err
			}
			lines = resp.Lines
		}

		if jsonOut {
			return printJSON(map[string]any{"lines": lines})
		}
		for _, line := range lines {
			fmt.Println(line)
		}
		return nil
	},
}

// logsFollow streams live log output for a service until EOF or interrupt.
func logsFollow(service string) error {
	client, err := apiStreamClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("http://aurelia/v1/services/%s/logs?follow=true", service), nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to daemon: %w (is aurelia daemon running?)", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("API error %d: %s", resp.StatusCode, body)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
	return scanner.Err()
}

// checkSpecDrift loads the daemon config, resolves the source spec directory,
// and prints a warning if any deployed specs have drifted from source.
func checkSpecDrift() {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return // config load failure is not fatal for status display
	}
	sourceDir := cfg.SpecSourceDir()
	if sourceDir == "" {
		return
	}
	deployedDir := defaultSpecDir()
	drifted, err := spec.DetectDrift(deployedDir, sourceDir)
	if err != nil || len(drifted) == 0 {
		return
	}

	changed := 0
	for _, d := range drifted {
		if d.Changed {
			changed++
		}
	}
	missing := len(drifted) - changed

	var parts []string
	if changed > 0 {
		parts = append(parts, fmt.Sprintf("%d changed", changed))
	}
	if missing > 0 {
		parts = append(parts, fmt.Sprintf("%d new in source", missing))
	}

	fmt.Fprintf(os.Stderr, "\nWARNING: %d service spec(s) out of sync with source (%s). Run 'just aurelia-sync' to update.\n",
		len(drifted), strings.Join(parts, ", "))
}

func init() {
	logsCmd.Flags().IntP("lines", "n", 50, "number of lines to show")
	logsCmd.Flags().BoolP("follow", "f", false, "stream live log output")
	deployCmd.Flags().String("drain", "5s", "drain period before stopping old instance")

	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(inspectCmd)
	rootCmd.AddCommand(shipCmd)
	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(deployCmd)
	rootCmd.AddCommand(reloadCmd)
	rootCmd.AddCommand(logsCmd)
}
