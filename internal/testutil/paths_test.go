package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindProjectRoot(t *testing.T) {
	root, err := FindProjectRoot()
	if err != nil {
		t.Fatalf("FindProjectRoot returned error: %v", err)
	}
	if root == "" {
		t.Fatal("FindProjectRoot returned empty string")
	}

	goMod := filepath.Join(root, "go.mod")
	if _, err := os.Stat(goMod); err != nil {
		t.Fatalf("go.mod not found at %s: %v", goMod, err)
	}
}
