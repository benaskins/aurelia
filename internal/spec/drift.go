package spec

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// DriftResult describes a single spec file that differs between deployed and source directories.
type DriftResult struct {
	Name       string // spec filename (e.g. "app-chat.yaml")
	DeployedIn bool   // true if present in deployed dir
	SourceIn   bool   // true if present in source dir
	Changed    bool   // true if both present but content differs
}

// DetectDrift compares spec files in the deployed directory against a source directory.
// It uses raw file content hashes (SHA-256) so the comparison is independent of
// env var expansion or parse ordering. Returns nil if directories are in sync.
// Returns an error only for I/O failures, not for drift itself.
func DetectDrift(deployedDir, sourceDir string) ([]DriftResult, error) {
	if _, err := os.Stat(sourceDir); err != nil {
		return nil, fmt.Errorf("source spec directory: %w", err)
	}

	deployedFiles, err := specFiles(deployedDir)
	if err != nil {
		return nil, fmt.Errorf("listing deployed specs: %w", err)
	}

	sourceFiles, err := specFiles(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("listing source specs: %w", err)
	}

	// Build hash maps keyed by filename
	deployedHashes := make(map[string]string, len(deployedFiles))
	for _, path := range deployedFiles {
		h, err := fileHash(path)
		if err != nil {
			return nil, err
		}
		deployedHashes[filepath.Base(path)] = h
	}

	sourceHashes := make(map[string]string, len(sourceFiles))
	for _, path := range sourceFiles {
		h, err := fileHash(path)
		if err != nil {
			return nil, err
		}
		sourceHashes[filepath.Base(path)] = h
	}

	var results []DriftResult

	// Check deployed specs against source
	for name, dHash := range deployedHashes {
		sHash, inSource := sourceHashes[name]
		if !inSource {
			// Deployed but not in source — could be intentional, skip
			continue
		}
		if dHash != sHash {
			results = append(results, DriftResult{
				Name:       name,
				DeployedIn: true,
				SourceIn:   true,
				Changed:    true,
			})
		}
	}

	// Check source specs missing from deployed
	for name := range sourceHashes {
		if _, inDeployed := deployedHashes[name]; !inDeployed {
			results = append(results, DriftResult{
				Name:       name,
				DeployedIn: false,
				SourceIn:   true,
			})
		}
	}

	return results, nil
}

// specFiles returns all .yaml and .yml files in a directory.
func specFiles(dir string) ([]string, error) {
	yamlFiles, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	ymlFiles, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	if err != nil {
		return nil, err
	}
	return append(yamlFiles, ymlFiles...), nil
}

// fileHash returns the hex-encoded SHA-256 hash of a file's contents.
func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", path, err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}
