package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "aurelia",
	Short: "macOS-native process supervisor",
	Long: `macOS-native process supervisor — manages native processes and Docker
containers with dependency ordering, health checks, and automatic restarts.

--- aurelia is mother ---`,
	Version: version,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
