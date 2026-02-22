package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/benaskins/aurelia/internal/spec"
	"github.com/spf13/cobra"
)

type checkResult struct {
	Path  string `json:"path"`
	Name  string `json:"name,omitempty"`
	Type  string `json:"type,omitempty"`
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
}

var checkCmd = &cobra.Command{
	Use:   "check [file-or-dir]",
	Short: "Validate service spec files",
	Long:  "Parse and validate YAML service specs. Checks a specific file, a directory, or the default spec directory (~/.aurelia/services/).",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runCheck,
}

func init() {
	rootCmd.AddCommand(checkCmd)
}

func runCheck(cmd *cobra.Command, args []string) error {
	jsonOut, _ := cmd.Flags().GetBool("json")

	target := defaultSpecDir()
	if len(args) > 0 {
		target = args[0]
	}

	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("cannot access %s: %w", target, err)
	}

	var files []string
	if info.IsDir() {
		yamlFiles, _ := filepath.Glob(filepath.Join(target, "*.yaml"))
		ymlFiles, _ := filepath.Glob(filepath.Join(target, "*.yml"))
		files = append(yamlFiles, ymlFiles...)
		if len(files) == 0 {
			return fmt.Errorf("no YAML files found in %s", target)
		}
	} else {
		files = []string{target}
	}

	var results []checkResult
	var failed int
	for _, path := range files {
		s, err := spec.Load(path)
		if err != nil {
			results = append(results, checkResult{Path: path, Valid: false, Error: err.Error()})
			failed++
		} else {
			results = append(results, checkResult{Path: path, Name: s.Service.Name, Type: string(s.Service.Type), Valid: true})
		}
	}

	if jsonOut {
		return printJSON(results)
	}

	// Human-readable output
	for _, r := range results {
		if r.Valid {
			fmt.Printf("OK    %s (%s, %s)\n", r.Path, r.Name, r.Type)
		} else {
			fmt.Fprintf(os.Stderr, "FAIL  %s\n      %v\n", r.Path, r.Error)
		}
	}

	if len(files) > 1 {
		passed := len(files) - failed
		fmt.Printf("\n%d/%d specs valid\n", passed, len(files))
	}

	if failed > 0 {
		return fmt.Errorf("%d spec(s) failed validation", failed)
	}
	return nil
}

func defaultSpecDir() string {
	dir, err := aureliaHome()
	if err != nil {
		return "."
	}
	return filepath.Join(dir, "services")
}
