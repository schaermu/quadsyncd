package runstore

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schaermu/quadsyncd/internal/multirepo"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestGenerateRunID(t *testing.T) {
	id1, err := generateRunID()
	if err != nil {
		t.Fatalf("generateRunID: %v", err)
	}

	if id1 == "" {
		t.Fatal("expected non-empty run ID")
	}

	// Format: YYYYMMDD-HHMMSS-<6-char-hex>
	parts := strings.Split(id1, "-")
	if len(parts) != 3 {
		t.Errorf("expected 3 parts, got %d: %s", len(parts), id1)
	}

	if len(parts[0]) != 8 {
		t.Errorf("expected date part to be 8 chars, got %d: %s", len(parts[0]), parts[0])
	}

	if len(parts[1]) != 6 {
		t.Errorf("expected time part to be 6 chars, got %d: %s", len(parts[1]), parts[1])
	}

	if len(parts[2]) != 6 {
		t.Errorf("expected random suffix to be 6 chars, got %d: %s", len(parts[2]), parts[2])
	}

	// Verify uniqueness
	id2, err := generateRunID()
	if err != nil {
		t.Fatalf("generateRunID (second): %v", err)
	}

	if id1 == id2 {
		t.Errorf("expected unique IDs, got duplicate: %s", id1)
	}
}

func TestStore_CreateAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, testLogger())
	ctx := context.Background()

	meta := RunMeta{
		Kind:      RunKindSync,
		Trigger:   TriggerCLI,
		StartedAt: time.Now().UTC(),
		Status:    RunStatusRunning,
		DryRun:    false,
		Revisions: map[string]string{
			"https://github.com/test/repo": "abc123",
		},
		Conflicts: []ConflictSummary{},
		Summary:   map[string]interface{}{"add": 5, "update": 2, "delete": 1},
	}

	if err := store.Create(ctx, &meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if meta.ID == "" {
		t.Fatal("expected ID to be set after Create")
	}

	retrieved, err := store.Get(ctx, meta.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if retrieved.ID != meta.ID {
		t.Errorf("ID = %q, want %q", retrieved.ID, meta.ID)
	}
	if retrieved.Kind != RunKindSync {
		t.Errorf("Kind = %q, want sync", retrieved.Kind)
	}
	if retrieved.Trigger != TriggerCLI {
		t.Errorf("Trigger = %q, want cli", retrieved.Trigger)
	}
	if retrieved.Status != RunStatusRunning {
		t.Errorf("Status = %q, want running", retrieved.Status)
	}
	if retrieved.DryRun != false {
		t.Errorf("DryRun = %v, want false", retrieved.DryRun)
	}
	if len(retrieved.Revisions) != 1 {
		t.Errorf("Revisions count = %d, want 1", len(retrieved.Revisions))
	}
	if retrieved.Revisions["https://github.com/test/repo"] != "abc123" {
		t.Errorf("Revisions[repo] = %q, want abc123", retrieved.Revisions["https://github.com/test/repo"])
	}
}

func TestStore_Update(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, testLogger())
	ctx := context.Background()

	meta := RunMeta{
		Kind:      RunKindSync,
		Trigger:   TriggerWebhook,
		StartedAt: time.Now().UTC(),
		Status:    RunStatusRunning,
		Revisions: map[string]string{},
		Conflicts: []ConflictSummary{},
	}

	if err := store.Create(ctx, &meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Update status to success
	endedAt := time.Now().UTC()
	meta.Status = RunStatusSuccess
	meta.EndedAt = &endedAt

	if err := store.Update(ctx, &meta); err != nil {
		t.Fatalf("Update: %v", err)
	}

	retrieved, err := store.Get(ctx, meta.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if retrieved.Status != RunStatusSuccess {
		t.Errorf("Status = %q, want success", retrieved.Status)
	}
	if retrieved.EndedAt == nil {
		t.Fatal("expected EndedAt to be set")
	}
	if retrieved.EndedAt.IsZero() {
		t.Error("expected EndedAt to be non-zero")
	}
}

func TestStore_List(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, testLogger())
	ctx := context.Background()

	// Create multiple runs with different start times
	now := time.Now().UTC()
	runs := []RunMeta{
		{
			Kind:      RunKindSync,
			Trigger:   TriggerTimer,
			StartedAt: now.Add(-3 * time.Hour),
			Status:    RunStatusSuccess,
			Revisions: map[string]string{},
			Conflicts: []ConflictSummary{},
		},
		{
			Kind:      RunKindSync,
			Trigger:   TriggerCLI,
			StartedAt: now.Add(-1 * time.Hour),
			Status:    RunStatusSuccess,
			Revisions: map[string]string{},
			Conflicts: []ConflictSummary{},
		},
		{
			Kind:      RunKindSync,
			Trigger:   TriggerWebhook,
			StartedAt: now,
			Status:    RunStatusRunning,
			Revisions: map[string]string{},
			Conflicts: []ConflictSummary{},
		},
	}

	for _, meta := range runs {
		if err := store.Create(ctx, &meta); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	listed, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(listed) != 3 {
		t.Fatalf("List count = %d, want 3", len(listed))
	}

	// Verify sorted newest first
	if !listed[0].StartedAt.After(listed[1].StartedAt) {
		t.Error("expected runs sorted newest first")
	}
	if !listed[1].StartedAt.After(listed[2].StartedAt) {
		t.Error("expected runs sorted newest first")
	}
}

func TestStore_AppendLogAndReadLog(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, testLogger())
	ctx := context.Background()

	meta := RunMeta{
		Kind:      RunKindSync,
		Trigger:   TriggerCLI,
		StartedAt: time.Now().UTC(),
		Status:    RunStatusRunning,
		Revisions: map[string]string{},
		Conflicts: []ConflictSummary{},
	}

	if err := store.Create(ctx, &meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Append log records
	records := []map[string]interface{}{
		{"level": "info", "msg": "starting sync", "time": time.Now().Format(time.RFC3339Nano)},
		{"level": "info", "msg": "fetching repo", "time": time.Now().Format(time.RFC3339Nano)},
		{"level": "info", "msg": "sync complete", "time": time.Now().Format(time.RFC3339Nano)},
	}

	for _, record := range records {
		// Marshal to JSON before appending
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		if err := store.AppendLog(ctx, meta.ID, line); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}

	// Read logs back
	retrieved, err := store.ReadLog(ctx, meta.ID)
	if err != nil {
		t.Fatalf("ReadLog: %v", err)
	}

	if len(retrieved) != 3 {
		t.Fatalf("ReadLog count = %d, want 3", len(retrieved))
	}

	for i, record := range retrieved {
		if record["msg"] != records[i]["msg"] {
			t.Errorf("record[%d].msg = %q, want %q", i, record["msg"], records[i]["msg"])
		}
	}
}

func TestStore_ReadLog_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, testLogger())
	ctx := context.Background()

	meta := RunMeta{
		Kind:      RunKindSync,
		Trigger:   TriggerCLI,
		StartedAt: time.Now().UTC(),
		Status:    RunStatusRunning,
		Revisions: map[string]string{},
		Conflicts: []ConflictSummary{},
	}

	if err := store.Create(ctx, &meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Read logs without appending anything
	logs, err := store.ReadLog(ctx, meta.ID)
	if err != nil {
		t.Fatalf("ReadLog: %v", err)
	}

	if len(logs) != 0 {
		t.Errorf("expected empty log slice, got %d records", len(logs))
	}
}

func TestStore_WritePlanAndReadPlan(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, testLogger())
	ctx := context.Background()

	meta := RunMeta{
		Kind:      RunKindPlan,
		Trigger:   TriggerCLI,
		StartedAt: time.Now().UTC(),
		Status:    RunStatusRunning,
		Revisions: map[string]string{},
		Conflicts: []ConflictSummary{},
	}

	if err := store.Create(ctx, &meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	plan := Plan{
		Requested: PlanRequest{
			RepoURL: "https://github.com/test/repo",
			Ref:     "main",
		},
		Conflicts: []ConflictSummary{
			{
				MergeKey: "foo.container",
				Winner: EffectiveItemSummary{
					MergeKey:   "foo.container",
					SourceRepo: "https://github.com/test/repo1",
					SourceRef:  "main",
					SourceSHA:  "abc123",
				},
				Losers: []EffectiveItemSummary{
					{
						MergeKey:   "foo.container",
						SourceRepo: "https://github.com/test/repo2",
						SourceRef:  "main",
						SourceSHA:  "def456",
					},
				},
			},
		},
		Ops: []PlanOp{
			{
				Op:         "add",
				Path:       "foo.container",
				Unit:       "foo.service",
				SourceRepo: "https://github.com/test/repo",
				SourceRef:  "main",
				SourceSHA:  "abc123",
				AfterPath:  "foo.container.after",
			},
			{
				Op:         "update",
				Path:       "bar.container",
				Unit:       "bar.service",
				SourceRepo: "https://github.com/test/repo",
				SourceRef:  "main",
				SourceSHA:  "abc123",
				BeforePath: "bar.container.before",
				AfterPath:  "bar.container.after",
			},
			{
				Op:         "delete",
				Path:       "baz.container",
				Unit:       "baz.service",
				BeforePath: "baz.container.before",
			},
		},
	}

	if err := store.WritePlan(ctx, meta.ID, plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	retrieved, err := store.ReadPlan(ctx, meta.ID)
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}

	if retrieved.Requested.RepoURL != "https://github.com/test/repo" {
		t.Errorf("Requested.RepoURL = %q, want https://github.com/test/repo", retrieved.Requested.RepoURL)
	}

	if len(retrieved.Conflicts) != 1 {
		t.Fatalf("Conflicts count = %d, want 1", len(retrieved.Conflicts))
	}

	if len(retrieved.Ops) != 3 {
		t.Fatalf("Ops count = %d, want 3", len(retrieved.Ops))
	}

	if retrieved.Ops[0].Op != "add" {
		t.Errorf("Ops[0].Op = %q, want add", retrieved.Ops[0].Op)
	}
	if retrieved.Ops[1].Op != "update" {
		t.Errorf("Ops[1].Op = %q, want update", retrieved.Ops[1].Op)
	}
	if retrieved.Ops[2].Op != "delete" {
		t.Errorf("Ops[2].Op = %q, want delete", retrieved.Ops[2].Op)
	}
}

func TestStore_ReadPlan_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, testLogger())
	ctx := context.Background()

	meta := RunMeta{
		Kind:      RunKindSync,
		Trigger:   TriggerCLI,
		StartedAt: time.Now().UTC(),
		Status:    RunStatusRunning,
		Revisions: map[string]string{},
		Conflicts: []ConflictSummary{},
	}

	if err := store.Create(ctx, &meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := store.ReadPlan(ctx, meta.ID)
	if err == nil {
		t.Fatal("expected error reading missing plan.json")
	}

	if !strings.Contains(err.Error(), "plan not found") {
		t.Errorf("expected 'plan not found' error, got: %v", err)
	}
}

func TestStore_WriteArtifactAndReadArtifact(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, testLogger())
	ctx := context.Background()

	meta := RunMeta{
		Kind:      RunKindPlan,
		Trigger:   TriggerCLI,
		StartedAt: time.Now().UTC(),
		Status:    RunStatusRunning,
		Revisions: map[string]string{},
		Conflicts: []ConflictSummary{},
	}

	if err := store.Create(ctx, &meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	content := []byte("[Container]\nImage=nginx:latest\n")
	if err := store.WriteArtifact(ctx, meta.ID, "foo.container", content); err != nil {
		t.Fatalf("WriteArtifact: %v", err)
	}

	retrieved, err := store.ReadArtifact(ctx, meta.ID, "foo.container")
	if err != nil {
		t.Fatalf("ReadArtifact: %v", err)
	}

	if string(retrieved) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", string(retrieved), string(content))
	}
}

func TestStore_ReadArtifact_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, testLogger())
	ctx := context.Background()

	meta := RunMeta{
		Kind:      RunKindPlan,
		Trigger:   TriggerCLI,
		StartedAt: time.Now().UTC(),
		Status:    RunStatusRunning,
		Revisions: map[string]string{},
		Conflicts: []ConflictSummary{},
	}

	if err := store.Create(ctx, &meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := store.ReadArtifact(ctx, meta.ID, "missing.container")
	if err == nil {
		t.Fatal("expected error reading missing artifact")
	}

	if !strings.Contains(err.Error(), "artifact not found") {
		t.Errorf("expected 'artifact not found' error, got: %v", err)
	}
}

func TestStore_Prune(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, testLogger())
	ctx := context.Background()

	now := time.Now().UTC()
	runs := []RunMeta{
		{
			Kind:      RunKindSync,
			Trigger:   TriggerTimer,
			StartedAt: now.Add(-20 * 24 * time.Hour), // 20 days old
			Status:    RunStatusSuccess,
			Revisions: map[string]string{},
			Conflicts: []ConflictSummary{},
		},
		{
			Kind:      RunKindSync,
			Trigger:   TriggerTimer,
			StartedAt: now.Add(-10 * 24 * time.Hour), // 10 days old
			Status:    RunStatusSuccess,
			Revisions: map[string]string{},
			Conflicts: []ConflictSummary{},
		},
		{
			Kind:      RunKindSync,
			Trigger:   TriggerCLI,
			StartedAt: now.Add(-1 * time.Hour), // 1 hour old
			Status:    RunStatusSuccess,
			Revisions: map[string]string{},
			Conflicts: []ConflictSummary{},
		},
	}

	for _, meta := range runs {
		if err := store.Create(ctx, &meta); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	// Prune runs older than 14 days
	if err := store.Prune(ctx, 14*24*time.Hour); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	listed, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// Should have 2 runs left (10 days old and 1 hour old)
	if len(listed) != 2 {
		t.Fatalf("List count = %d, want 2", len(listed))
	}

	// Verify the old run was pruned
	for _, run := range listed {
		if run.StartedAt.Before(now.Add(-14 * 24 * time.Hour)) {
			t.Errorf("expected run to be pruned: %s (started at %v)", run.ID, run.StartedAt)
		}
	}
}

func TestStore_Prune_PartialRuns(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, testLogger())
	ctx := context.Background()

	now := time.Now().UTC()

	// Create a valid run
	validRun := RunMeta{
		Kind:      RunKindSync,
		Trigger:   TriggerTimer,
		StartedAt: now.Add(-20 * 24 * time.Hour),
		Status:    RunStatusSuccess,
		Revisions: map[string]string{},
		Conflicts: []ConflictSummary{},
	}
	if err := store.Create(ctx, &validRun); err != nil {
		t.Fatalf("Create valid run: %v", err)
	}

	// Create a partial/malformed run directory
	malformedID := "20240101-120000-abcdef"
	malformedDir := filepath.Join(store.baseDir, malformedID)
	if err := os.MkdirAll(malformedDir, 0755); err != nil {
		t.Fatalf("create malformed dir: %v", err)
	}

	// Prune should handle the malformed run gracefully
	if err := store.Prune(ctx, 14*24*time.Hour); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	// Verify valid run was pruned
	listed, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(listed) != 0 {
		t.Errorf("expected 0 runs after prune, got %d", len(listed))
	}

	// Verify malformed directory was actually removed
	if _, err := os.Stat(malformedDir); !os.IsNotExist(err) {
		t.Errorf("malformed directory still exists after prune: %s", malformedDir)
	}
}

func TestStore_MultiRepoMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, testLogger())
	ctx := context.Background()

	// Create a run with multi-repo metadata
	meta := RunMeta{
		Kind:      RunKindSync,
		Trigger:   TriggerWebhook,
		StartedAt: time.Now().UTC(),
		Status:    RunStatusSuccess,
		Revisions: map[string]string{
			"https://github.com/org/repo1": "abc123",
			"https://github.com/org/repo2": "def456",
			"https://github.com/org/repo3": "789ghi",
		},
		Conflicts: []ConflictSummary{
			{
				MergeKey: "conflicted.container",
				Winner: EffectiveItemSummary{
					MergeKey:   "conflicted.container",
					SourceRepo: "https://github.com/org/repo1",
					SourceRef:  "main",
					SourceSHA:  "abc123",
				},
				Losers: []EffectiveItemSummary{
					{
						MergeKey:   "conflicted.container",
						SourceRepo: "https://github.com/org/repo2",
						SourceRef:  "main",
						SourceSHA:  "def456",
					},
					{
						MergeKey:   "conflicted.container",
						SourceRepo: "https://github.com/org/repo3",
						SourceRef:  "dev",
						SourceSHA:  "789ghi",
					},
				},
			},
		},
	}

	if err := store.Create(ctx, &meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	retrieved, err := store.Get(ctx, meta.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Verify revisions round-trip
	if len(retrieved.Revisions) != 3 {
		t.Fatalf("Revisions count = %d, want 3", len(retrieved.Revisions))
	}

	expectedRevisions := map[string]string{
		"https://github.com/org/repo1": "abc123",
		"https://github.com/org/repo2": "def456",
		"https://github.com/org/repo3": "789ghi",
	}

	for url, sha := range expectedRevisions {
		if retrieved.Revisions[url] != sha {
			t.Errorf("Revisions[%s] = %q, want %q", url, retrieved.Revisions[url], sha)
		}
	}

	// Verify conflicts round-trip
	if len(retrieved.Conflicts) != 1 {
		t.Fatalf("Conflicts count = %d, want 1", len(retrieved.Conflicts))
	}

	conflict := retrieved.Conflicts[0]
	if conflict.MergeKey != "conflicted.container" {
		t.Errorf("Conflict.MergeKey = %q, want conflicted.container", conflict.MergeKey)
	}

	if conflict.Winner.SourceRepo != "https://github.com/org/repo1" {
		t.Errorf("Winner.SourceRepo = %q, want repo1", conflict.Winner.SourceRepo)
	}

	if len(conflict.Losers) != 2 {
		t.Fatalf("Losers count = %d, want 2", len(conflict.Losers))
	}
}

func TestConflictSummaryFromMultirepo(t *testing.T) {
	conflict := multirepo.Conflict{
		MergeKey: "test.container",
		Winner: multirepo.EffectiveItem{
			MergeKey:   "test.container",
			AbsPath:    "/tmp/repo1/test.container",
			SourceRepo: "https://github.com/org/repo1",
			SourceRef:  "main",
			SourceSHA:  "abc123",
		},
		Losers: []multirepo.EffectiveItem{
			{
				MergeKey:   "test.container",
				AbsPath:    "/tmp/repo2/test.container",
				SourceRepo: "https://github.com/org/repo2",
				SourceRef:  "dev",
				SourceSHA:  "def456",
			},
		},
	}

	summary := ConflictSummaryFromMultirepo(conflict)

	if summary.MergeKey != "test.container" {
		t.Errorf("MergeKey = %q, want test.container", summary.MergeKey)
	}

	if summary.Winner.SourceRepo != "https://github.com/org/repo1" {
		t.Errorf("Winner.SourceRepo = %q, want repo1", summary.Winner.SourceRepo)
	}

	if len(summary.Losers) != 1 {
		t.Fatalf("Losers count = %d, want 1", len(summary.Losers))
	}

	if summary.Losers[0].SourceRepo != "https://github.com/org/repo2" {
		t.Errorf("Losers[0].SourceRepo = %q, want repo2", summary.Losers[0].SourceRepo)
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, testLogger())
	ctx := context.Background()

	_, err := store.Get(ctx, "nonexistent-run-id")
	if err == nil {
		t.Fatal("expected error getting nonexistent run")
	}

	if !strings.Contains(err.Error(), "run not found") {
		t.Errorf("expected 'run not found' error, got: %v", err)
	}
}

func TestStore_List_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir, testLogger())
	ctx := context.Background()

	runs, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(runs) != 0 {
		t.Errorf("expected empty list, got %d runs", len(runs))
	}
}
