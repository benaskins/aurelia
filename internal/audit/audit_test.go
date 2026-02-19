package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoggerWritesEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	l, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	defer l.Close()

	ts := time.Date(2026, 2, 19, 10, 30, 0, 0, time.UTC)

	l.Log(Entry{
		Timestamp: ts,
		Action:    ActionSecretRead,
		Key:       "chat/database-url",
		Service:   "chat",
		Trigger:   "service_start",
	})

	l.Log(Entry{
		Timestamp: ts.Add(time.Hour),
		Action:    ActionSecretWrite,
		Key:       "chat/api-key",
		Actor:     "cli",
	})

	// Read and verify
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	var e1 Entry
	json.Unmarshal([]byte(lines[0]), &e1)
	if e1.Action != ActionSecretRead {
		t.Errorf("expected secret_read, got %v", e1.Action)
	}
	if e1.Key != "chat/database-url" {
		t.Errorf("expected chat/database-url, got %q", e1.Key)
	}
	if e1.Service != "chat" {
		t.Errorf("expected chat, got %q", e1.Service)
	}

	var e2 Entry
	json.Unmarshal([]byte(lines[1]), &e2)
	if e2.Action != ActionSecretWrite {
		t.Errorf("expected secret_write, got %v", e2.Action)
	}
	if e2.Actor != "cli" {
		t.Errorf("expected cli, got %q", e2.Actor)
	}
}

func TestLoggerAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")

	// Write first entry, close
	l1, _ := NewLogger(path)
	l1.Log(Entry{Action: ActionSecretWrite, Key: "first"})
	l1.Close()

	// Open again, write second entry
	l2, _ := NewLogger(path)
	l2.Log(Entry{Action: ActionSecretRead, Key: "second"})
	l2.Close()

	// Both entries should be present
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
}

func TestLoggerDefaultTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	l, _ := NewLogger(path)
	defer l.Close()

	before := time.Now().UTC()
	l.Log(Entry{Action: ActionSecretRead, Key: "test"})
	after := time.Now().UTC()

	data, _ := os.ReadFile(path)
	var e Entry
	json.Unmarshal(data, &e)

	if e.Timestamp.Before(before) || e.Timestamp.After(after) {
		t.Errorf("timestamp %v not between %v and %v", e.Timestamp, before, after)
	}
}

func TestLoggerFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	l, _ := NewLogger(path)
	l.Close()

	info, _ := os.Stat(path)
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected 0600, got %o", perm)
	}
}
