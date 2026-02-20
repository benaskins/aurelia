//go:build darwin

package main

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

const launchAgentLabel = "com.aurelia.daemon"

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install aurelia as a LaunchAgent (starts on login)",
	RunE: func(cmd *cobra.Command, args []string) error {
		binary, err := os.Executable()
		if err != nil {
			return fmt.Errorf("finding binary path: %w", err)
		}

		// Resolve symlinks to get the real path
		binary, err = filepath.EvalSymlinks(binary)
		if err != nil {
			return fmt.Errorf("resolving binary path: %w", err)
		}

		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("finding home dir: %w", err)
		}

		plistDir := filepath.Join(home, "Library", "LaunchAgents")
		plistPath := filepath.Join(plistDir, launchAgentLabel+".plist")
		logPath := filepath.Join(home, ".aurelia", "daemon.log")

		if err := os.MkdirAll(plistDir, 0755); err != nil {
			return fmt.Errorf("creating LaunchAgents dir: %w", err)
		}

		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>daemon</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, launchAgentLabel, html.EscapeString(binary), html.EscapeString(logPath), html.EscapeString(logPath))

		if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
			return fmt.Errorf("writing plist: %w", err)
		}

		// Load the LaunchAgent
		if err := exec.Command("launchctl", "load", plistPath).Run(); err != nil {
			return fmt.Errorf("launchctl load: %w", err)
		}

		fmt.Printf("Installed LaunchAgent: %s\n", plistPath)
		fmt.Printf("Binary: %s\n", binary)
		fmt.Printf("Logs: %s\n", logPath)
		fmt.Println("aurelia daemon will start now and on every login.")
		return nil
	},
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall the aurelia LaunchAgent",
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("finding home dir: %w", err)
		}

		plistPath := filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")

		// Unload first (ignore errors — may not be loaded)
		_ = exec.Command("launchctl", "unload", plistPath).Run()

		if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing plist: %w", err)
		}

		fmt.Println("Uninstalled aurelia LaunchAgent.")
		fmt.Println("aurelia daemon will no longer start on login.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(uninstallCmd)
}
