package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// FindProjectRoot walks up the directory tree from the current file to find go.mod
func FindProjectRoot() (string, error) {
	// Get the directory of the caller's source file
	_, filename, _, ok := runtime.Caller(1)
	if !ok {
		return "", fmt.Errorf("failed to get caller information")
	}

	dir := filepath.Dir(filename)

	// Walk up the directory tree looking for go.mod
	for {
		goModPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the root without finding go.mod
			return "", fmt.Errorf("go.mod not found in any parent directory")
		}
		dir = parent
	}
}
