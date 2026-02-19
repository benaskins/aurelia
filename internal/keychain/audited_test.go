package keychain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benaskins/aurelia/internal/audit"
)

func setupAuditedStore(t *testing.T) (*AuditedStore, string) {
	t.Helper()
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "audit.log")
	metaPath := filepath.Join(dir, "secret-metadata.json")

	auditLog, err := audit.NewLogger(auditPath)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	t.Cleanup(func() { auditLog.Close() })

	meta, err := NewMetadataStore(metaPath)
	if err != nil {
		t.Fatalf("NewMetadataStore: %v", err)
	}

	inner := NewMemoryStore()
	store := NewAuditedStore(inner, auditLog, meta, "cli")

	return store, auditPath
}

func readAuditEntries(t *testing.T, path string) []audit.Entry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	entries := make([]audit.Entry, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var e audit.Entry
		json.Unmarshal([]byte(line), &e)
		entries = append(entries, e)
	}
	return entries
}

func TestAuditedStoreSetLogsWrite(t *testing.T) {
	store, auditPath := setupAuditedStore(t)

	store.Set("test/key", "value")

	entries := readAuditEntries(t, auditPath)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Action != audit.ActionSecretWrite {
		t.Errorf("expected secret_write, got %v", entries[0].Action)
	}
	if entries[0].Key != "test/key" {
		t.Errorf("expected test/key, got %q", entries[0].Key)
	}
	if entries[0].Actor != "cli" {
		t.Errorf("expected cli, got %q", entries[0].Actor)
	}
}

func TestAuditedStoreGetLogsRead(t *testing.T) {
	store, auditPath := setupAuditedStore(t)

	store.Set("test/get", "val")
	store.Get("test/get")

	entries := readAuditEntries(t, auditPath)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[1].Action != audit.ActionSecretRead {
		t.Errorf("expected secret_read, got %v", entries[1].Action)
	}
}

func TestAuditedStoreDeleteLogsDelete(t *testing.T) {
	store, auditPath := setupAuditedStore(t)

	store.Set("test/del", "val")
	store.Delete("test/del")

	entries := readAuditEntries(t, auditPath)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[1].Action != audit.ActionSecretDelete {
		t.Errorf("expected secret_delete, got %v", entries[1].Action)
	}
}

func TestAuditedStoreGetForService(t *testing.T) {
	store, auditPath := setupAuditedStore(t)

	store.Set("chat/db-url", "postgres://...")
	store.GetForService("chat/db-url", "chat")

	entries := readAuditEntries(t, auditPath)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[1].Service != "chat" {
		t.Errorf("expected service chat, got %q", entries[1].Service)
	}
	if entries[1].Trigger != "service_start" {
		t.Errorf("expected trigger service_start, got %q", entries[1].Trigger)
	}
}

func TestAuditedStoreRotate(t *testing.T) {
	store, auditPath := setupAuditedStore(t)

	store.Set("test/rotate", "old-value")

	err := store.Rotate("test/rotate", "echo new-value")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// Verify the value was updated
	val, err := store.Get("test/rotate")
	if err != nil {
		t.Fatalf("Get after rotate: %v", err)
	}
	if val != "new-value" {
		t.Errorf("expected 'new-value', got %q", val)
	}

	// Verify audit entry
	entries := readAuditEntries(t, auditPath)
	// set + rotate + get = 3 entries
	rotateEntries := filterEntries(entries, audit.ActionSecretRotate)
	if len(rotateEntries) != 1 {
		t.Fatalf("expected 1 rotate entry, got %d", len(rotateEntries))
	}
	if rotateEntries[0].Command != "echo new-value" {
		t.Errorf("expected command 'echo new-value', got %q", rotateEntries[0].Command)
	}

	// Verify metadata was updated
	meta := store.Metadata().Get("test/rotate")
	if meta == nil {
		t.Fatal("expected metadata")
	}
	if meta.LastRotated.IsZero() {
		t.Error("expected LastRotated to be set")
	}
}

func TestAuditedStoreRotateFailure(t *testing.T) {
	store, auditPath := setupAuditedStore(t)

	store.Set("test/rotate-fail", "original")

	err := store.Rotate("test/rotate-fail", "exit 1")
	if err == nil {
		t.Error("expected error from failing rotation command")
	}

	// Value should be preserved
	val, _ := store.Get("test/rotate-fail")
	if val != "original" {
		t.Errorf("expected original value preserved, got %q", val)
	}

	// Audit should log the failure
	entries := readAuditEntries(t, auditPath)
	rotateEntries := filterEntries(entries, audit.ActionSecretRotate)
	if len(rotateEntries) != 1 {
		t.Fatalf("expected 1 rotate entry, got %d", len(rotateEntries))
	}
	if rotateEntries[0].Error == "" {
		t.Error("expected error in audit entry")
	}
}

func TestMetadataStorePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")

	// Write
	ms1, _ := NewMetadataStore(path)
	ms1.Set("key1", &SecretMetadata{RotateEvery: "30d"})

	// Read back
	ms2, _ := NewMetadataStore(path)
	meta := ms2.Get("key1")
	if meta == nil {
		t.Fatal("expected metadata after reload")
	}
	if meta.RotateEvery != "30d" {
		t.Errorf("expected 30d, got %q", meta.RotateEvery)
	}
}

func filterEntries(entries []audit.Entry, action audit.Action) []audit.Entry {
	var result []audit.Entry
	for _, e := range entries {
		if e.Action == action {
			result = append(result, e)
		}
	}
	return result
}
