package sync

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/schaermu/quadsyncd/internal/config"
)

func TestFileHash(t *testing.T) {
	// Create a temp file
	tmpfile, err := os.CreateTemp("", "test-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(tmpfile.Name())
	}()

	content := "test content"
	if _, err := tmpfile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	// Compute hash
	hash1, err := fileHash(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Verify hash is consistent
	hash2, err := fileHash(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}

	if hash1 != hash2 {
		t.Errorf("hash mismatch: %s != %s", hash1, hash2)
	}

	// Verify hash changes when content changes
	if err := os.WriteFile(tmpfile.Name(), []byte("different content"), 0644); err != nil {
		t.Fatal(err)
	}

	hash3, err := fileHash(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}

	if hash1 == hash3 {
		t.Error("hash should change when content changes")
	}
}

func TestBuildPlan(t *testing.T) {
	// Create temporary directories
	tmpDir, err := os.MkdirTemp("", "quadsyncd-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	quadletDir := filepath.Join(tmpDir, "quadlet")
	stateDir := filepath.Join(tmpDir, "state")

	// Create repo subdirectory so RepoDir() method works correctly
	repoDir := filepath.Join(stateDir, "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Copy test file to repo location
	repoTestFile := filepath.Join(repoDir, "test.container")
	if err := os.WriteFile(repoTestFile, []byte("test content"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Repo: config.RepoConfig{
			Subdir: "",
		},
		Paths: config.PathsConfig{
			QuadletDir: quadletDir,
			StateDir:   stateDir,
		},
		Sync: config.SyncConfig{
			Prune:   true,
			Restart: config.RestartChanged,
		},
	}

	// Create a discard logger for tests
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError, // Only log errors during tests
	}))

	engine := &Engine{
		cfg:    cfg,
		logger: logger,
	}

	// Build plan with no previous state
	prevState := &State{ManagedFiles: make(map[string]ManagedFile)}
	plan, err := engine.buildPlan(prevState)
	if err != nil {
		t.Fatal(err)
	}

	// Should have one add operation
	if len(plan.Add) != 1 {
		t.Errorf("expected 1 add operation, got %d", len(plan.Add))
	}
	if len(plan.Update) != 0 {
		t.Errorf("expected 0 update operations, got %d", len(plan.Update))
	}
	if len(plan.Delete) != 0 {
		t.Errorf("expected 0 delete operations, got %d", len(plan.Delete))
	}
}
