package main

import (
	"context"
	crypto_tls "crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/benaskins/aurelia/internal/api"
	"github.com/benaskins/aurelia/internal/config"
	"github.com/benaskins/aurelia/internal/daemon"
	"github.com/benaskins/aurelia/internal/gpu"
	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the aurelia daemon",
	Long:  "Start the process supervisor daemon. Loads service specs and manages their lifecycle.",
	RunE:  runDaemon,
}

var (
	apiAddr       string
	routingOutput string
	daemonForce   bool
)

func init() {
	daemonCmd.Flags().StringVar(&apiAddr, "api-addr", "", "Optional TCP address for API (e.g. 127.0.0.1:9090)")
	daemonCmd.Flags().StringVar(&routingOutput, "routing-output", "", "Path to write Traefik dynamic config (enables routing)")
	daemonCmd.Flags().BoolVar(&daemonForce, "force", false, "Bypass launchd safety check for manual daemon start")
	rootCmd.AddCommand(daemonCmd)
}

func runDaemon(cmd *cobra.Command, args []string) error {
	// Safety check: warn/block manual starts when a LaunchAgent is installed
	if warning, err := launchdCheck(daemonForce); err != nil {
		return err
	} else if warning != "" {
		slog.Warn(warning)
	}

	specDir := defaultSpecDir()

	// Ensure spec directory exists
	if err := os.MkdirAll(specDir, 0700); err != nil {
		return fmt.Errorf("creating spec dir: %w", err)
	}

	// Load config file (missing file is not an error)
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config %s: %w", cfgPath, err)
	}

	// CLI flags override config file values
	if routingOutput == "" && cfg.RoutingOutput != "" {
		routingOutput = cfg.RoutingOutput
		slog.Info("routing-output from config file", "path", routingOutput)
	} else if routingOutput != "" {
		slog.Info("routing-output from CLI flag", "path", routingOutput)
	}

	if apiAddr == "" && cfg.APIAddr != "" {
		apiAddr = cfg.APIAddr
		slog.Info("api-addr from config file", "addr", apiAddr)
	} else if apiAddr != "" {
		slog.Info("api-addr from CLI flag", "addr", apiAddr)
	}

	slog.Info("aurelia daemon starting", "spec_dir", specDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Create and start daemon with secret store
	stateDir := filepath.Dir(specDir)
	secrets, err := newSecretStore("daemon")
	if err != nil {
		return fmt.Errorf("creating secret store: %w", err)
	}
	opts := []daemon.Option{daemon.WithSecrets(secrets), daemon.WithStateDir(stateDir)}
	if routingOutput != "" {
		opts = append(opts, daemon.WithRouting(routingOutput))
		slog.Info("routing enabled", "output", routingOutput)
	}
	// Load TLS config if configured (used for both peer connections and TCP listener)
	var serverTLS *crypto_tls.Config
	var peerTLS *crypto_tls.Config
	if cfg.TLS.Configured() {
		serverTLS, err = api.LoadTLSConfig(cfg.TLS.Cert, cfg.TLS.Key, cfg.TLS.CA)
		if err != nil {
			return fmt.Errorf("loading TLS config: %w", err)
		}
		// Peer TLS uses the same cert/key as client cert for mTLS
		peerTLS, err = api.LoadPeerTLSConfig(cfg.TLS.Cert, cfg.TLS.Key, cfg.TLS.CA)
		if err != nil {
			return fmt.Errorf("loading peer TLS config: %w", err)
		}
		slog.Info("TLS configured for API and peer connections")
	}

	// Wire up spec source for drift detection
	if specSource := cfg.SpecSourceDir(); specSource != "" {
		opts = append(opts, daemon.WithSpecSource(specSource))
		slog.Info("spec drift detection enabled", "source", specSource)
	}

	// Wire up peer nodes from config
	if len(cfg.Nodes) > 0 {
		peers := daemon.BuildPeers(cfg, peerTLS)
		if len(peers) > 0 {
			opts = append(opts, daemon.WithPeers(peers))
			slog.Info("peer nodes configured", "count", len(peers))
		}
	}
	d := daemon.NewDaemon(specDir, opts...)
	if err := d.Start(ctx); err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}

	// Start API server
	socketPath, err := defaultSocketPath()
	if err != nil {
		return err
	}
	// Check if another daemon is already running
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err == nil {
		conn.Close()
		return fmt.Errorf("another daemon is already running (socket %s is active)", socketPath)
	}
	// Stale socket — safe to remove
	os.Remove(socketPath)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		return fmt.Errorf("creating socket dir: %w", err)
	}

	// Start GPU observer
	gpuObs := gpu.NewObserver(5 * time.Second)
	gpuObs.Start(ctx)

	srv := api.NewServer(d, gpuObs)
	if cfg.NodeName != "" {
		srv.SetNodeName(cfg.NodeName)
	}
	if cfg.LaminaRoot != "" {
		srv.SetLaminaRoot(cfg.LaminaRoot)
		slog.Info("lamina workspace configured", "root", cfg.LaminaRoot)
	}

	// Start API in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenUnix(socketPath)
	}()

	// Optionally start TCP API with auth
	if apiAddr != "" {
		tokenPath := filepath.Join(filepath.Dir(socketPath), "api.token")
		if err := srv.GenerateToken(tokenPath); err != nil {
			return fmt.Errorf("generating API token: %w", err)
		}
		if serverTLS != nil {
			go func() {
				if err := srv.ListenTLS(apiAddr, serverTLS); err != nil {
					slog.Error("TLS API error", "error", err)
				}
			}()
		} else {
			slog.Warn("TCP API running without TLS, bearer token sent in plaintext")
			go func() {
				if err := srv.ListenTCP(apiAddr); err != nil {
					slog.Error("TCP API error", "error", err)
				}
			}()
		}
	}

	slog.Info("aurelia daemon ready")

	// Wait for signal or error
	var receivedSig os.Signal
	select {
	case sig := <-sigCh:
		slog.Info("received signal, shutting down", "signal", sig)
		receivedSig = sig
	case err := <-errCh:
		if err != nil {
			slog.Error("API server error", "error", err)
		}
	}

	// Graceful shutdown — differentiate SIGTERM (orphan children) vs SIGINT (full teardown)
	if receivedSig == syscall.SIGTERM {
		// SIGTERM: release supervision first (while context is still alive),
		// then cancel context. Native children survive because NativeDriver
		// uses exec.Command (not CommandContext).
		d.Shutdown(daemon.DefaultStopTimeout)
		cancel()
	} else {
		// SIGINT, API error, or any other case: full teardown
		cancel()
		d.Stop(daemon.DefaultStopTimeout)
	}
	srv.Shutdown(context.Background())
	os.Remove(socketPath)

	slog.Info("aurelia daemon stopped")
	return nil
}

func defaultSocketPath() (string, error) {
	dir, err := aureliaHome()
	if err != nil {
		return "", fmt.Errorf("cannot determine socket path: %w", err)
	}
	return filepath.Join(dir, "aurelia.sock"), nil
}
