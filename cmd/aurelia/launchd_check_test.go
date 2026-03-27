package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchdCheck_NoPlist(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	homeDir := func() (string, error) { return home, nil }
	getenv := func(string) string { return "" }

	warning, err := launchdCheckEnv(homeDir, getenv)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if warning != "" {
		t.Fatalf("expected no warning, got: %s", warning)
	}
}

func TestLaunchdCheck_PlistExists_StartedByLaunchd(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	plistDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(plistDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plistDir, launchdPlistName), []byte("<plist/>"), 0644); err != nil {
		t.Fatal(err)
	}

	homeDir := func() (string, error) { return home, nil }
	getenv := func(key string) string {
		if key == "XPC_SERVICE_NAME" {
			return "com.aurelia.daemon"
		}
		return ""
	}

	warning, err := launchdCheckEnv(homeDir, getenv)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if warning != "" {
		t.Fatalf("expected no warning when started by launchd, got: %s", warning)
	}
}

func TestLaunchdCheck_PlistExists_ManualStart_NoAureliaRoot(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	plistDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(plistDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plistDir, launchdPlistName), []byte("<plist/>"), 0644); err != nil {
		t.Fatal(err)
	}

	homeDir := func() (string, error) { return home, nil }
	getenv := func(string) string { return "" }

	warning, err := launchdCheckEnv(homeDir, getenv)
	if warning != "" {
		t.Fatalf("expected no warning on error path, got: %s", warning)
	}
	if err == nil {
		t.Fatal("expected error when AURELIA_ROOT is missing")
	}
	if !strings.Contains(err.Error(), "AURELIA_ROOT is not set") {
		t.Fatalf("error should mention AURELIA_ROOT, got: %v", err)
	}
	if !strings.Contains(err.Error(), "launchctl kickstart") {
		t.Fatalf("error should mention launchctl kickstart, got: %v", err)
	}
}

func TestLaunchdCheck_PlistExists_ManualStart_WithAureliaRoot(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	plistDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(plistDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plistDir, launchdPlistName), []byte("<plist/>"), 0644); err != nil {
		t.Fatal(err)
	}

	homeDir := func() (string, error) { return home, nil }
	getenv := func(key string) string {
		if key == "AURELIA_ROOT" {
			return "/opt/aurelia"
		}
		return ""
	}

	warning, err := launchdCheckEnv(homeDir, getenv)
	if err != nil {
		t.Fatalf("expected no error when AURELIA_ROOT is set, got: %v", err)
	}
	if warning == "" {
		t.Fatal("expected a warning for manual start with LaunchAgent present")
	}
	if !strings.Contains(warning, "launchctl kickstart") {
		t.Fatalf("warning should mention launchctl kickstart, got: %s", warning)
	}
}

func TestLaunchdCheck_ForceBypass(t *testing.T) {
	t.Helper()
	// force=true should skip all checks, even if we can't test the full path
	warning, err := launchdCheck(true)
	if err != nil {
		t.Fatalf("expected no error with --force, got: %v", err)
	}
	if warning != "" {
		t.Fatalf("expected no warning with --force, got: %s", warning)
	}
}
