//go:build !darwin

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install aurelia as a LaunchAgent (macOS only)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("LaunchAgent installation is only available on macOS")
	},
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall the aurelia LaunchAgent (macOS only)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("LaunchAgent installation is only available on macOS")
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(uninstallCmd)
}
