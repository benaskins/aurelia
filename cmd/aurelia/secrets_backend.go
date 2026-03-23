package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/benaskins/aurelia/internal/audit"
	"github.com/benaskins/aurelia/internal/config"
	"github.com/benaskins/aurelia/internal/keychain"
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

	inner := resolveBackend(dir)
	return keychain.NewAuditedStore(inner, auditLog, meta, actor), nil
}

// resolveBackend picks the best available secrets backend.
func resolveBackend(stateDir string) keychain.Store {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Warn("failed to load config, using keychain", "error", err)
		return keychain.NewSystemStore()
	}

	if cfg.OpenBao != nil {
		token, err := cfg.OpenBao.LoadToken()
		if err != nil {
			slog.Warn("openbao token not available, using keychain", "error", err)
			return keychain.NewSystemStore()
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
			slog.Warn("openbao unreachable, using keychain", "addr", cfg.OpenBao.Addr, "error", err)
			return keychain.NewSystemStore()
		}

		slog.Info("secrets backend: openbao", "addr", cfg.OpenBao.Addr)
		return store
	}

	return keychain.NewSystemStore()
}
