package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/benaskins/aurelia/internal/spec"
	"github.com/spf13/cobra"
)

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
	target := defaultSpecDir()
	if len(args) > 0 {
		target = args[0]
	}

	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("cannot access %s: %w", target, err)
	}

	if info.IsDir() {
		return checkDir(target)
	}
	return checkFile(target)
}

func checkFile(path string) error {
	s, err := spec.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL  %s\n      %v\n", path, err)
		return fmt.Errorf("validation failed")
	}
	fmt.Printf("OK    %s (%s, %s)\n", path, s.Service.Name, s.Service.Type)
	return nil
}

func checkDir(dir string) error {
	yamlFiles, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	ymlFiles, _ := filepath.Glob(filepath.Join(dir, "*.yml"))
	files := append(yamlFiles, ymlFiles...)

	if len(files) == 0 {
		return fmt.Errorf("no YAML files found in %s", dir)
	}

	var failed int
	for _, path := range files {
		s, err := spec.Load(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL  %s\n      %v\n", path, err)
			failed++
			continue
		}
		fmt.Printf("OK    %s (%s, %s)\n", path, s.Service.Name, s.Service.Type)
	}

	total := len(files)
	passed := total - failed
	fmt.Printf("\n%d/%d specs valid\n", passed, total)

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
