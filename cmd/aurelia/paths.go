package main

import (
	"os"
	"path/filepath"
)

// aureliaHome returns the path to the aurelia home directory (~/.aurelia).
func aureliaHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aurelia"), nil
}
