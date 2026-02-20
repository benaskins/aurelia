package keychain

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/benaskins/aurelia/internal/audit"
)

// SecretMetadata tracks rotation and staleness info for a secret.
type SecretMetadata struct {
	CreatedAt   time.Time `json:"created_at"`
	LastRotated time.Time `json:"last_rotated,omitempty"`
	RotateEvery string    `json:"rotate_every,omitempty"`
}

// MetadataStore persists secret metadata to a JSON file.
type MetadataStore struct {
	mu       sync.RWMutex
	path     string
	metadata map[string]*SecretMetadata
}

// NewMetadataStore loads or creates a metadata file.
func NewMetadataStore(path string) (*MetadataStore, error) {
	ms := &MetadataStore{
		path:     path,
		metadata: make(map[string]*SecretMetadata),
	}

	data, err := os.ReadFile(path)
	if err == nil {
		if jsonErr := json.Unmarshal(data, &ms.metadata); jsonErr != nil {
			slog.Warn("corrupt metadata file, starting fresh", "path", path, "error", jsonErr)
		}
	}
	// File not existing is fine — start fresh.

	return ms, nil
}

// Get returns a copy of the metadata for a key, or nil if not tracked.
// Returning a copy prevents callers from mutating the store's internal state
// without holding the lock (data race).
func (ms *MetadataStore) Get(key string) *SecretMetadata {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	m, ok := ms.metadata[key]
	if !ok {
		return nil
	}
	cp := *m
	return &cp
}

// Set records metadata for a key and persists to disk.
func (ms *MetadataStore) Set(key string, meta *SecretMetadata) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.metadata[key] = meta
	return ms.save()
}

// Delete removes metadata for a key.
func (ms *MetadataStore) Delete(key string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	delete(ms.metadata, key)
	return ms.save()
}

// All returns copies of all metadata entries.
// Each value is a deep copy to prevent callers from mutating internal state
// without holding the lock (data race).
func (ms *MetadataStore) All() map[string]*SecretMetadata {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	result := make(map[string]*SecretMetadata, len(ms.metadata))
	for k, v := range ms.metadata {
		cp := *v
		result[k] = &cp
	}
	return result
}

func (ms *MetadataStore) save() error {
	data, err := json.MarshalIndent(ms.metadata, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := ms.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmpPath, ms.path)
}

// AuditedStore wraps a Store and adds audit logging and metadata tracking.
type AuditedStore struct {
	inner    Store
	audit    *audit.Logger
	metadata *MetadataStore
	actor    string // "cli" or "daemon"
}

// NewAuditedStore wraps an existing store with audit logging.
func NewAuditedStore(inner Store, auditLog *audit.Logger, metadata *MetadataStore, actor string) *AuditedStore {
	return &AuditedStore{
		inner:    inner,
		audit:    auditLog,
		metadata: metadata,
		actor:    actor,
	}
}

func (s *AuditedStore) Set(key, value string) error {
	if err := s.inner.Set(key, value); err != nil {
		return fmt.Errorf("audited store set: %w", err)
	}

	// Audit logging is best-effort — a failure to log should not block the operation.
	s.audit.Log(audit.Entry{
		Action: audit.ActionSecretWrite,
		Key:    key,
		Actor:  s.actor,
	})

	now := time.Now().UTC()
	meta := s.metadata.Get(key)
	if meta == nil {
		meta = &SecretMetadata{CreatedAt: now}
	}
	if err := s.metadata.Set(key, meta); err != nil {
		return fmt.Errorf("saving metadata: %w", err)
	}

	return nil
}

func (s *AuditedStore) Get(key string) (string, error) {
	val, err := s.inner.Get(key)
	if err != nil {
		return "", fmt.Errorf("audited store get: %w", err)
	}

	// Audit logging is best-effort — a failure to log should not block the operation.
	s.audit.Log(audit.Entry{
		Action: audit.ActionSecretRead,
		Key:    key,
		Actor:  s.actor,
	})

	return val, nil
}

func (s *AuditedStore) List() ([]string, error) {
	return s.inner.List()
}

func (s *AuditedStore) Delete(key string) error {
	if err := s.inner.Delete(key); err != nil {
		return fmt.Errorf("audited store delete: %w", err)
	}

	// Audit logging is best-effort — a failure to log should not block the operation.
	s.audit.Log(audit.Entry{
		Action: audit.ActionSecretDelete,
		Key:    key,
		Actor:  s.actor,
	})

	if err := s.metadata.Delete(key); err != nil {
		return fmt.Errorf("deleting metadata: %w", err)
	}
	return nil
}

func (s *AuditedStore) GetMultiple(keys []string) (map[string]string, error) {
	result, err := s.inner.GetMultiple(keys)
	if err != nil {
		return nil, fmt.Errorf("audited store get multiple: %w", err)
	}

	for key := range result {
		// Audit logging is best-effort — a failure to log should not block the operation.
		s.audit.Log(audit.Entry{
			Action: audit.ActionSecretRead,
			Key:    key,
			Actor:  s.actor,
		})
	}

	return result, nil
}

// GetForService retrieves a secret and logs it as a service-start read.
func (s *AuditedStore) GetForService(key, service string) (string, error) {
	val, err := s.inner.Get(key)
	if err != nil {
		return "", fmt.Errorf("audited store get for service: %w", err)
	}

	// Audit logging is best-effort — a failure to log should not block the operation.
	s.audit.Log(audit.Entry{
		Action:  audit.ActionSecretRead,
		Key:     key,
		Service: service,
		Actor:   "daemon",
		Trigger: "service_start",
	})

	return val, nil
}

// Rotate runs a rotation command, captures its output, stores the new value,
// and logs the rotation.
func (s *AuditedStore) Rotate(key, command string) error {
	// Run the rotation command and capture stdout.
	output, err := runRotationCommand(command)
	if err != nil {
		// Audit logging is best-effort — a failure to log should not block the operation.
		s.audit.Log(audit.Entry{
			Action:  audit.ActionSecretRotate,
			Key:     key,
			Actor:   s.actor,
			Trigger: "hook",
			Command: command,
			Error:   err.Error(),
		})
		return fmt.Errorf("rotation command failed: %w", err)
	}

	// Store the new value
	if err := s.inner.Set(key, output); err != nil {
		return fmt.Errorf("storing rotated secret: %w", err)
	}

	// Audit logging is best-effort — a failure to log should not block the operation.
	s.audit.Log(audit.Entry{
		Action:  audit.ActionSecretRotate,
		Key:     key,
		Actor:   s.actor,
		Trigger: "hook",
		Command: command,
	})

	// Update metadata
	now := time.Now().UTC()
	meta := s.metadata.Get(key)
	if meta == nil {
		meta = &SecretMetadata{CreatedAt: now}
	}
	meta.LastRotated = now
	if err := s.metadata.Set(key, meta); err != nil {
		return fmt.Errorf("saving rotation metadata: %w", err)
	}

	return nil
}

// Metadata returns the metadata store for direct access.
func (s *AuditedStore) Metadata() *MetadataStore {
	return s.metadata
}
