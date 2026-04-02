package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/benaskins/aurelia/internal/api"
	"github.com/benaskins/aurelia/internal/audit"
	"github.com/benaskins/aurelia/internal/config"
	"github.com/benaskins/aurelia/internal/keychain"
	"github.com/benaskins/aurelia/internal/node"
)

// newSecretStore creates the secret store using the configured backend.
// It prefers OpenBao when configured and reachable, falling back to macOS Keychain.
func newSecretStore(actor string) (*keychain.AuditedStore, error) {
	dir, err := aureliaHome()
	if err != nil {
		return nil, fmt.Errorf("finding aurelia home: %w", err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating directory: %w", err)
	}

	auditLog, err := audit.NewLogger(filepath.Join(dir, "audit.log"))
	if err != nil {
		return nil, err
	}

	meta, err := keychain.NewMetadataStore(filepath.Join(dir, "secret-metadata.json"))
	if err != nil {
		return nil, err
	}

	inner, err := resolveBackend(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving secrets backend: %w", err)
	}
	return keychain.NewAuditedStore(inner, auditLog, meta, actor), nil
}

// resolveBackend picks the best available secrets backend.
// When OpenBao is configured, it is required — no silent fallback to Keychain.
func resolveBackend(stateDir string) (keychain.Store, error) {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("loading config %s: %w", cfgPath, err)
	}

	if cfg.OpenBao != nil {
		token, err := cfg.OpenBao.LoadToken()
		if err != nil {
			return nil, fmt.Errorf("openbao configured but token not available: %w", err)
		}

		var opts []keychain.BaoOption
		if cfg.OpenBao.UnsealFile != "" {
			opts = append(opts, keychain.WithUnsealFile(cfg.OpenBao.UnsealFile))
		}

		mount := cfg.OpenBao.Mount
		if mount == "" {
			mount = "secret"
		}

		store := keychain.NewBaoStore(cfg.OpenBao.Addr, token, mount, opts...)
		if err := store.Ping(); err != nil {
			return nil, fmt.Errorf("openbao configured but unreachable at %s: %w", cfg.OpenBao.Addr, err)
		}

		slog.Info("secrets backend: openbao", "addr", cfg.OpenBao.Addr)
		return store, nil
	}

	if cfg.OpenBaoPeer != nil {
		peer, err := buildPeerClient(cfg, cfg.OpenBaoPeer.Peer)
		if err != nil {
			return nil, fmt.Errorf("openbao_peer: %w", err)
		}

		mount := cfg.OpenBaoPeer.Mount
		if mount == "" {
			mount = "secret"
		}

		store := keychain.NewPeerBaoStore(cfg.OpenBaoPeer.Addr, mount, func() (string, error) {
			resp, err := peer.RequestBaoToken()
			if err != nil {
				return "", err
			}
			return resp.Token, nil
		})

		slog.Info("secrets backend: openbao via peer",
			"peer", cfg.OpenBaoPeer.Peer, "addr", cfg.OpenBaoPeer.Addr)
		return store, nil
	}

	return keychain.NewSystemStore(), nil
}

// waitForSecretStore retries newSecretStore until it succeeds or the context is cancelled.
// Used when the daemon starts before OpenBao is ready.
func waitForSecretStore(ctx context.Context, actor string) (*keychain.AuditedStore, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
			store, err := newSecretStore(actor)
			if err == nil {
				return store, nil
			}
			slog.Debug("secrets backend not ready, retrying", "error", err)
		}
	}
}

// resolvePKICertIssuer creates a BaoPKIIssuer for certificate operations.
// PKI issuance requires elevated privileges, so this uses either:
//   - an explicit vault token (--vault-token flag)
//   - the root token from AURELIA_ROOT/.vault-keys
//   - the direct OpenBao config token (if running on the Vault node)
func resolvePKICertIssuer(mount, vaultToken string) (*keychain.BaoPKIIssuer, error) {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("loading config %s: %w", cfgPath, err)
	}

	// Resolve the OpenBao address
	var addr string
	if cfg.OpenBao != nil {
		addr = cfg.OpenBao.Addr
	} else if cfg.OpenBaoPeer != nil {
		addr = cfg.OpenBaoPeer.Addr
	} else {
		return nil, fmt.Errorf("no openbao backend configured")
	}

	// Resolve the token: flag > .vault-keys file > config
	if vaultToken == "" {
		vaultToken = loadVaultKeysToken()
	}
	if vaultToken == "" && cfg.OpenBao != nil {
		vaultToken, _ = cfg.OpenBao.LoadToken()
	}
	if vaultToken == "" {
		return nil, fmt.Errorf("no vault token available (use --vault-token or set AURELIA_ROOT)")
	}

	store := keychain.NewBaoStore(addr, vaultToken, mount)
	return keychain.NewBaoPKIIssuer(store, mount), nil
}

// loadVaultKeysToken reads the root token from AURELIA_ROOT/.vault-keys.
func loadVaultKeysToken() string {
	root := os.Getenv("AURELIA_ROOT")
	if root == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, ".vault-keys"))
	if err != nil {
		return ""
	}
	var keys struct {
		RootToken string `json:"root_token"`
	}
	if err := json.Unmarshal(data, &keys); err != nil {
		return ""
	}
	return keys.RootToken
}

func buildPeerClient(cfg *config.Config, peerName string) (*node.Client, error) {
	n, ok := cfg.FindNode(peerName)
	if !ok {
		return nil, fmt.Errorf("peer %q not found in config nodes", peerName)
	}
	token, err := n.LoadToken()
	if err != nil {
		return nil, fmt.Errorf("loading token for peer %s: %w", peerName, err)
	}
	if cfg.TLS.Configured() {
		tlsCfg, err := api.LoadPeerTLSConfig(cfg.TLS.Cert, cfg.TLS.Key, cfg.TLS.CA)
		if err != nil {
			return nil, fmt.Errorf("loading TLS for peer %s: %w", peerName, err)
		}
		return node.NewTLS(n.Name, n.Addr, token, tlsCfg), nil
	}
	return node.New(n.Name, n.Addr, token), nil
}
