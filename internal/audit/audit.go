// Package audit provides append-only structured logging for secret operations.
//
// Every secret access (read, write, delete, rotate) is recorded to an audit
// log at ~/.aurelia/audit.log as newline-delimited JSON.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Action describes what happened.
type Action string

const (
	ActionSecretRead   Action = "secret_read"
	ActionSecretWrite  Action = "secret_write"
	ActionSecretDelete Action = "secret_delete"
	ActionSecretRotate Action = "secret_rotate"
)

// Entry is a single audit log record.
type Entry struct {
	Timestamp time.Time `json:"ts"`
	Action    Action    `json:"action"`
	Key       string    `json:"key"`
	Service   string    `json:"service,omitempty"`
	Actor     string    `json:"actor,omitempty"`   // "cli", "daemon", "rotation"
	Trigger   string    `json:"trigger,omitempty"` // "service_start", "manual", "hook"
	Command   string    `json:"command,omitempty"` // rotation command if applicable
	Error     string    `json:"error,omitempty"`
}

// Logger writes audit entries to an append-only file.
type Logger struct {
	mu   sync.Mutex
	file *os.File
	path string
}

// NewLogger creates or opens an audit log file for appending.
func NewLogger(path string) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening audit log: %w", err)
	}
	return &Logger{file: f, path: path}, nil
}

// Log writes an audit entry.
func (l *Logger) Log(entry Entry) error {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshaling audit entry: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, err := l.file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("writing audit entry: %w", err)
	}
	return nil
}

// Close closes the audit log file.
func (l *Logger) Close() error {
	return l.file.Close()
}
