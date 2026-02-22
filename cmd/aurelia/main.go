package main

import (
	"encoding/json"
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

func init() {
	rootCmd.PersistentFlags().Bool("json", false, "Output in JSON format")
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
