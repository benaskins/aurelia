package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogWriter_WritesToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	w := LogWriter(path)
	t.Cleanup(func() { _ = w.Close() })

	if _, err := w.Write([]byte("hello aurelia\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "hello aurelia") {
		t.Fatalf("log file missing written content, got %q", data)
	}
}

func TestLogWriter_RetainsBoundedBackups(t *testing.T) {
	w := LogWriter(filepath.Join(t.TempDir(), "daemon.log"))
	t.Cleanup(func() { _ = w.Close() })

	// Left at zero, lumberjack keeps every rotated file forever — the unbounded
	// growth this change exists to prevent.
	if w.MaxBackups <= 0 {
		t.Errorf("MaxBackups must bound retained files, got %d", w.MaxBackups)
	}
}
