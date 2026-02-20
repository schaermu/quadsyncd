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
	tmpDir := t.TempDir()
	tmpPath := filepath.Join(tmpDir, "test.txt")

	content := "test content"
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Compute hash
	hash1, err := fileHash(tmpPath)
	if err != nil {
		t.Fatal(err)
	}

	// Verify hash is consistent
	hash2, err := fileHash(tmpPath)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 != hash2 {
		t.Errorf("hash mismatch: %s != %s", hash1, hash2)
	}

	// Verify hash changes when content changes
	if err := os.WriteFile(tmpPath, []byte("different content"), 0644); err != nil {
		t.Fatal(err)
	}

	hash3, err := fileHash(tmpPath)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 == hash3 {
		t.Error("hash should change when content changes")
	}
}

func TestBuildPlan(t *testing.T) {
	// Create temporary directories
	tmpDir := t.TempDir()

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

func TestBuildPlan_CompanionFiles(t *testing.T) {
	tmpDir := t.TempDir()

	quadletDir := filepath.Join(tmpDir, "quadlet")
	stateDir := filepath.Join(tmpDir, "state")

	repoDir := filepath.Join(stateDir, "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a quadlet file and a companion env file side-by-side in the repo
	if err := os.WriteFile(filepath.Join(repoDir, "myapp.container"), []byte("[Container]\nImage=alpine\nEnvironmentFile=./myapp.env\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "myapp.env"), []byte("FOO=bar\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Repo: config.RepoConfig{Subdir: ""},
		Paths: config.PathsConfig{
			QuadletDir: quadletDir,
			StateDir:   stateDir,
		},
		Sync: config.SyncConfig{Prune: false, Restart: config.RestartChanged},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	engine := &Engine{cfg: cfg, logger: logger}

	prevState := &State{ManagedFiles: make(map[string]ManagedFile)}
	plan, err := engine.buildPlan(prevState)
	if err != nil {
		t.Fatal(err)
	}

	// Both the quadlet file and the companion env file should be in the add list
	if len(plan.Add) != 2 {
		t.Errorf("expected 2 add operations (quadlet + companion), got %d", len(plan.Add))
	}

	// Verify the companion file destination path is in the quadlet dir
	foundEnv := false
	for _, op := range plan.Add {
		if filepath.Base(op.DestPath) == "myapp.env" {
			foundEnv = true
			expectedDest := filepath.Join(quadletDir, "myapp.env")
			if op.DestPath != expectedDest {
				t.Errorf("companion file dest = %s, want %s", op.DestPath, expectedDest)
			}
		}
	}
	if !foundEnv {
		t.Error("companion env file not found in add plan")
	}
}

func TestAllManagedUnits_IncludesUnchanged(t *testing.T) {
	cfg := &config.Config{
		Sync: config.SyncConfig{Restart: config.RestartAllManaged},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	engine := &Engine{cfg: cfg, logger: logger}

	// State has two managed quadlet files and one companion file.
	state := &State{
		Commit: "abc",
		ManagedFiles: map[string]ManagedFile{
			"/quadlet/app.container": {SourcePath: "app.container", Hash: "aaa"},
			"/quadlet/db.container":  {SourcePath: "db.container", Hash: "bbb"},
			"/quadlet/app.env":       {SourcePath: "app.env", Hash: "ccc"},
		},
	}

	units := engine.allManagedUnits(state)

	// Expect two units (one per quadlet file); companion files are not units.
	if len(units) != 2 {
		t.Fatalf("allManagedUnits() returned %d units, want 2: %v", len(units), units)
	}

	want := map[string]bool{"app.service": true, "db.service": true}
	for _, u := range units {
		if !want[u] {
			t.Errorf("unexpected unit %q in allManagedUnits result", u)
		}
	}
}

func TestQuadletUnitsFromOps(t *testing.T) {
	ops := []FileOp{
		{DestPath: "/quadlet/app.container"},
		{DestPath: "/quadlet/app.env"}, // companion, not a unit
		{DestPath: "/quadlet/db.container"},
		{DestPath: "/quadlet/app.container"}, // duplicate
	}

	units := quadletUnitsFromOps(ops)

	if len(units) != 2 {
		t.Fatalf("quadletUnitsFromOps() returned %d units, want 2: %v", len(units), units)
	}

	want := map[string]bool{"app.service": true, "db.service": true}
	for _, u := range units {
		if !want[u] {
			t.Errorf("unexpected unit %q", u)
		}
	}
}
