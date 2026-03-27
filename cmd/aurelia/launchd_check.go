package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const launchdPlistName = "com.aurelia.daemon.plist"

// launchdCheck verifies that a manual daemon start is safe when a LaunchAgent
// plist is registered. It returns a warning string (non-fatal) and an error
// (fatal, daemon should not start).
//
// The check is skipped entirely when force is true.
func launchdCheck(force bool) (warning string, err error) {
	if force {
		return "", nil
	}
	return launchdCheckEnv(os.UserHomeDir, os.Getenv)
}

// launchdCheckEnv is the testable core of launchdCheck.
func launchdCheckEnv(homeDir func() (string, error), getenv func(string) string) (warning string, err error) {
	home, err := homeDir()
	if err != nil {
		// Can't determine home dir — skip the check rather than block startup.
		return "", nil
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchdPlistName)
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		// No LaunchAgent installed — nothing to warn about.
		return "", nil
	}

	// LaunchAgent plist exists. Check if we were started by launchd.
	if getenv("XPC_SERVICE_NAME") == "com.aurelia.daemon" {
		// Started by launchd — all good.
		return "", nil
	}

	// Manual start with LaunchAgent present. Check AURELIA_ROOT.
	if getenv("AURELIA_ROOT") == "" {
		return "", fmt.Errorf("ERROR: AURELIA_ROOT is not set. The aurelia daemon must be started via launchd to get required environment variables. Use: launchctl kickstart gui/$(id -u)/com.aurelia.daemon")
	}

	return "WARNING: aurelia daemon should be started via launchd, not manually. Use: launchctl kickstart gui/$(id -u)/com.aurelia.daemon. Manual start may be missing required environment variables (AURELIA_ROOT).", nil
}
