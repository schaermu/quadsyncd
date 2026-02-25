package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/runstore"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestRunSync_CreatesRunRecord(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a minimal config
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	cfgContent := `
repository:
  url: https://github.com/test/repo
  ref: main

paths:
  quadlet_dir: ` + filepath.Join(tmpDir, "quadlets") + `
  state_dir: ` + filepath.Join(tmpDir, "state") + `

sync:
  prune: false
  restart: none
`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Load config to verify it's valid
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Create runstore
	store := runstore.NewStore(cfg.Paths.StateDir, testLogger())

	// Create initial run metadata
	meta := &runstore.RunMeta{
		Kind:      runstore.RunKindSync,
		Trigger:   runstore.TriggerCLI,
		Status:    runstore.RunStatusRunning,
		DryRun:    false,
		Revisions: make(map[string]string),
		Conflicts: []runstore.ConflictSummary{},
	}

	ctx := context.Background()
	if err := store.Create(ctx, meta); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	if meta.ID == "" {
		t.Fatal("expected non-empty run ID")
	}

	// Verify meta.json was created
	metaPath := filepath.Join(cfg.Paths.StateDir, "runs", meta.ID, "meta.json")
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("meta.json not found: %v", err)
	}

	// Read back and verify
	retrieved, err := store.Get(ctx, meta.ID)
	if err != nil {
		t.Fatalf("failed to get run: %v", err)
	}

	if retrieved.Status != runstore.RunStatusRunning {
		t.Errorf("status = %s, want running", retrieved.Status)
	}
	if retrieved.Kind != runstore.RunKindSync {
		t.Errorf("kind = %s, want sync", retrieved.Kind)
	}
}

func TestRunSync_LogStreamingToNDJSON(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a minimal config
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	cfgContent := `
repository:
  url: https://github.com/test/repo
  ref: main

paths:
  quadlet_dir: ` + filepath.Join(tmpDir, "quadlets") + `
  state_dir: ` + filepath.Join(tmpDir, "state") + `

sync:
  prune: false
  restart: none
`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Create runstore
	store := runstore.NewStore(cfg.Paths.StateDir, testLogger())

	// Create run
	meta := &runstore.RunMeta{
		Kind:      runstore.RunKindSync,
		Trigger:   runstore.TriggerCLI,
		Status:    runstore.RunStatusRunning,
		DryRun:    false,
		Revisions: make(map[string]string),
		Conflicts: []runstore.ConflictSummary{},
	}

	ctx := context.Background()
	if err := store.Create(ctx, meta); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	// Append some log lines
	log1 := map[string]interface{}{
		"time":  "2024-01-01T00:00:00Z",
		"level": "INFO",
		"msg":   "first log message",
	}
	log2 := map[string]interface{}{
		"time":  "2024-01-01T00:00:01Z",
		"level": "WARN",
		"msg":   "second log message",
	}

	line1, _ := json.Marshal(log1)
	line2, _ := json.Marshal(log2)

	if err := store.AppendLog(ctx, meta.ID, line1); err != nil {
		t.Fatalf("failed to append log: %v", err)
	}
	if err := store.AppendLog(ctx, meta.ID, line2); err != nil {
		t.Fatalf("failed to append log: %v", err)
	}

	// Read logs back
	logs, err := store.ReadLog(ctx, meta.ID)
	if err != nil {
		t.Fatalf("failed to read logs: %v", err)
	}

	if len(logs) != 2 {
		t.Fatalf("expected 2 log records, got %d", len(logs))
	}

	if logs[0]["msg"] != "first log message" {
		t.Errorf("first log msg = %v, want 'first log message'", logs[0]["msg"])
	}
	if logs[1]["msg"] != "second log message" {
		t.Errorf("second log msg = %v, want 'second log message'", logs[1]["msg"])
	}
}

func TestRunSync_FinalizeWithRevisionsAndConflicts(t *testing.T) {
	tmpDir := t.TempDir()

	cfgPath := filepath.Join(tmpDir, "config.yaml")
	cfgContent := `
repository:
  url: https://github.com/test/repo
  ref: main

paths:
  quadlet_dir: ` + filepath.Join(tmpDir, "quadlets") + `
  state_dir: ` + filepath.Join(tmpDir, "state") + `

sync:
  prune: false
  restart: none
`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	store := runstore.NewStore(cfg.Paths.StateDir, testLogger())

	// Create run
	meta := &runstore.RunMeta{
		Kind:      runstore.RunKindSync,
		Trigger:   runstore.TriggerCLI,
		Status:    runstore.RunStatusRunning,
		DryRun:    false,
		Revisions: make(map[string]string),
		Conflicts: []runstore.ConflictSummary{},
	}

	ctx := context.Background()
	if err := store.Create(ctx, meta); err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	// Finalize with revisions and conflicts
	meta.Status = runstore.RunStatusSuccess
	meta.Revisions = map[string]string{
		"https://github.com/test/repo": "abc123",
	}
	meta.Conflicts = []runstore.ConflictSummary{
		{
			MergeKey: "path/to/file.container",
			Winner: runstore.EffectiveItemSummary{
				MergeKey:   "path/to/file.container",
				SourceRepo: "https://github.com/test/repo",
				SourceRef:  "main",
				SourceSHA:  "abc123",
			},
			Losers: []runstore.EffectiveItemSummary{
				{
					MergeKey:   "path/to/file.container",
					SourceRepo: "https://github.com/other/repo",
					SourceRef:  "main",
					SourceSHA:  "def456",
				},
			},
		},
	}

	if err := store.Update(ctx, meta); err != nil {
		t.Fatalf("failed to update run: %v", err)
	}

	// Read back and verify
	retrieved, err := store.Get(ctx, meta.ID)
	if err != nil {
		t.Fatalf("failed to get run: %v", err)
	}

	if retrieved.Status != runstore.RunStatusSuccess {
		t.Errorf("status = %s, want success", retrieved.Status)
	}

	if len(retrieved.Revisions) != 1 {
		t.Fatalf("expected 1 revision, got %d", len(retrieved.Revisions))
	}
	if retrieved.Revisions["https://github.com/test/repo"] != "abc123" {
		t.Errorf("revision = %s, want abc123", retrieved.Revisions["https://github.com/test/repo"])
	}

	if len(retrieved.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(retrieved.Conflicts))
	}
	if retrieved.Conflicts[0].MergeKey != "path/to/file.container" {
		t.Errorf("conflict merge_key = %s, want path/to/file.container", retrieved.Conflicts[0].MergeKey)
	}
}
