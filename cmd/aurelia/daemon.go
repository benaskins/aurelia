package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/benaskins/aurelia/internal/api"
	"github.com/benaskins/aurelia/internal/daemon"
	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the aurelia daemon",
	Long:  "Start the process supervisor daemon. Loads service specs and manages their lifecycle.",
	RunE:  runDaemon,
}

var apiAddr string

func init() {
	daemonCmd.Flags().StringVar(&apiAddr, "api-addr", "", "Optional TCP address for API (e.g. 127.0.0.1:9090)")
	rootCmd.AddCommand(daemonCmd)
}

func runDaemon(cmd *cobra.Command, args []string) error {
	specDir := defaultSpecDir()

	// Ensure spec directory exists
	if err := os.MkdirAll(specDir, 0755); err != nil {
		return fmt.Errorf("creating spec dir: %w", err)
	}

	slog.Info("aurelia daemon starting", "spec_dir", specDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Create and start daemon
	d := daemon.NewDaemon(specDir)
	if err := d.Start(ctx); err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}

	// Start API server
	socketPath := defaultSocketPath()
	// Remove stale socket
	os.Remove(socketPath)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		return fmt.Errorf("creating socket dir: %w", err)
	}

	srv := api.NewServer(d, ctx)

	// Start API in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenUnix(socketPath)
	}()

	// Optionally start TCP API
	if apiAddr != "" {
		go func() {
			if err := srv.ListenTCP(apiAddr); err != nil {
				slog.Error("TCP API error", "error", err)
			}
		}()
	}

	slog.Info("aurelia daemon ready")

	// Wait for signal or error
	select {
	case sig := <-sigCh:
		slog.Info("received signal, shutting down", "signal", sig)
	case err := <-errCh:
		if err != nil {
			slog.Error("API server error", "error", err)
		}
	}

	// Graceful shutdown
	cancel()
	d.Stop(30)
	srv.Shutdown(context.Background())
	os.Remove(socketPath)

	slog.Info("aurelia daemon stopped")
	return nil
}

func defaultSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/aurelia.sock"
	}
	return filepath.Join(home, ".aurelia", "aurelia.sock")
}
