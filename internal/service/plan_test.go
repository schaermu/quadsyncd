package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/schaermu/quadsyncd/internal/runstore"
	quadsyncd "github.com/schaermu/quadsyncd/internal/sync"
	"github.com/schaermu/quadsyncd/internal/testutil"
)

// newTestPlanRequest returns a minimal PlanRequest for use in tests.
func newTestPlanRequest() runstore.PlanRequest {
	return runstore.PlanRequest{}
}

// TestWritePlanWithArtifacts_QuadletAdd verifies that an "add" operation for a
// .container quadlet file produces a non-empty AfterPath artifact and a
// populated Unit name.
func TestWritePlanWithArtifacts_QuadletAdd(t *testing.T) {
	tmpDir := t.TempDir()
	quadletDir := filepath.Join(tmpDir, "quadlets")
	srcDir := filepath.Join(tmpDir, "src")

	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	srcFile := filepath.Join(srcDir, "app.container")
	if err := os.WriteFile(srcFile, []byte("[Container]\nImage=nginx\n"), 0644); err != nil {
		t.Fatal(err)
	}

	syncPlan := &quadsyncd.Plan{
		Add: []quadsyncd.FileOp{
			{
				SourcePath: srcFile,
				DestPath:   filepath.Join(quadletDir, "app.container"),
				SourceRepo: "https://github.com/test/repo",
				SourceRef:  "refs/heads/main",
				SourceSHA:  "abc123",
			},
		},
		Update: []quadsyncd.FileOp{},
		Delete: []quadsyncd.FileOp{},
	}

	store := testutil.NewMockRunStore()
	logger := testutil.TestLogger()
	ctx := context.Background()

	plan := writePlanWithArtifacts(ctx, store, "run-1", syncPlan, nil, quadletDir, newTestPlanRequest(), logger)

	if len(plan.Ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(plan.Ops))
	}
	op := plan.Ops[0]
	if op.Op != "add" {
		t.Errorf("expected op=add, got %q", op.Op)
	}
	if op.Unit == "" {
		t.Error("expected Unit to be set for .container file")
	}
	if op.Unit != "app.service" {
		t.Errorf("expected Unit=app.service, got %q", op.Unit)
	}
	if op.AfterPath == "" {
		t.Error("expected AfterPath to be populated for add op")
	}
	if op.BeforePath != "" {
		t.Error("expected BeforePath to be empty for add op")
	}

	// Confirm artifact was written to the store.
	if _, ok := store.Artifacts["run-1"]; !ok {
		t.Error("expected artifacts to be written to store")
	}
}

// TestWritePlanWithArtifacts_QuadletUpdate verifies that an "update" operation
// for a .container file writes both before (on-disk) and after (source)
// artifacts, and populates the Unit field.
func TestWritePlanWithArtifacts_QuadletUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	quadletDir := filepath.Join(tmpDir, "quadlets")
	srcDir := filepath.Join(tmpDir, "src")

	if err := os.MkdirAll(quadletDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	destFile := filepath.Join(quadletDir, "db.container")
	if err := os.WriteFile(destFile, []byte("[Container]\nImage=postgres:14\n"), 0644); err != nil {
		t.Fatal(err)
	}

	srcFile := filepath.Join(srcDir, "db.container")
	if err := os.WriteFile(srcFile, []byte("[Container]\nImage=postgres:15\n"), 0644); err != nil {
		t.Fatal(err)
	}

	syncPlan := &quadsyncd.Plan{
		Add: []quadsyncd.FileOp{},
		Update: []quadsyncd.FileOp{
			{
				SourcePath: srcFile,
				DestPath:   destFile,
			},
		},
		Delete: []quadsyncd.FileOp{},
	}

	store := testutil.NewMockRunStore()
	logger := testutil.TestLogger()
	ctx := context.Background()

	plan := writePlanWithArtifacts(ctx, store, "run-2", syncPlan, nil, quadletDir, newTestPlanRequest(), logger)

	if len(plan.Ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(plan.Ops))
	}
	op := plan.Ops[0]
	if op.Op != "update" {
		t.Errorf("expected op=update, got %q", op.Op)
	}
	if op.Unit == "" {
		t.Error("expected Unit to be set")
	}
	if op.BeforePath == "" {
		t.Error("expected BeforePath to be populated for update op")
	}
	if op.AfterPath == "" {
		t.Error("expected AfterPath to be populated for update op")
	}

	// Both artifacts must be stored.
	arts := store.Artifacts["run-2"]
	if _, ok := arts[op.BeforePath]; !ok {
		t.Errorf("before artifact %q not found in store", op.BeforePath)
	}
	if _, ok := arts[op.AfterPath]; !ok {
		t.Errorf("after artifact %q not found in store", op.AfterPath)
	}
}

// TestWritePlanWithArtifacts_QuadletDelete verifies that a "delete" operation
// writes only a BeforePath artifact and leaves AfterPath empty.
func TestWritePlanWithArtifacts_QuadletDelete(t *testing.T) {
	tmpDir := t.TempDir()
	quadletDir := filepath.Join(tmpDir, "quadlets")

	if err := os.MkdirAll(quadletDir, 0755); err != nil {
		t.Fatal(err)
	}

	destFile := filepath.Join(quadletDir, "cache.container")
	if err := os.WriteFile(destFile, []byte("[Container]\nImage=redis\n"), 0644); err != nil {
		t.Fatal(err)
	}

	syncPlan := &quadsyncd.Plan{
		Add:    []quadsyncd.FileOp{},
		Update: []quadsyncd.FileOp{},
		Delete: []quadsyncd.FileOp{
			{DestPath: destFile},
		},
	}

	store := testutil.NewMockRunStore()
	logger := testutil.TestLogger()
	ctx := context.Background()

	plan := writePlanWithArtifacts(ctx, store, "run-3", syncPlan, nil, quadletDir, newTestPlanRequest(), logger)

	if len(plan.Ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(plan.Ops))
	}
	op := plan.Ops[0]
	if op.Op != "delete" {
		t.Errorf("expected op=delete, got %q", op.Op)
	}
	if op.BeforePath == "" {
		t.Error("expected BeforePath to be populated for delete op")
	}
	if op.AfterPath != "" {
		t.Errorf("expected AfterPath to be empty for delete op, got %q", op.AfterPath)
	}
}

// TestWritePlanWithArtifacts_CompanionFilesSkipped verifies that non-quadlet
// companion files (.env, .yaml) produce ops without Unit or artifacts.
func TestWritePlanWithArtifacts_CompanionFilesSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	quadletDir := filepath.Join(tmpDir, "quadlets")
	srcDir := filepath.Join(tmpDir, "src")

	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	envFile := filepath.Join(srcDir, "app.env")
	if err := os.WriteFile(envFile, []byte("FOO=bar\n"), 0644); err != nil {
		t.Fatal(err)
	}
	yamlFile := filepath.Join(srcDir, "config.yaml")
	if err := os.WriteFile(yamlFile, []byte("key: value\n"), 0644); err != nil {
		t.Fatal(err)
	}

	syncPlan := &quadsyncd.Plan{
		Add: []quadsyncd.FileOp{
			{
				SourcePath: envFile,
				DestPath:   filepath.Join(quadletDir, "app.env"),
			},
			{
				SourcePath: yamlFile,
				DestPath:   filepath.Join(quadletDir, "config.yaml"),
			},
		},
		Update: []quadsyncd.FileOp{},
		Delete: []quadsyncd.FileOp{},
	}

	store := testutil.NewMockRunStore()
	logger := testutil.TestLogger()
	ctx := context.Background()

	plan := writePlanWithArtifacts(ctx, store, "run-4", syncPlan, nil, quadletDir, newTestPlanRequest(), logger)

	if len(plan.Ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(plan.Ops))
	}
	for _, op := range plan.Ops {
		if op.Unit != "" {
			t.Errorf("companion file produced a non-empty Unit: %q", op.Unit)
		}
		if op.AfterPath != "" {
			t.Errorf("companion file produced an AfterPath artifact: %q", op.AfterPath)
		}
		if op.BeforePath != "" {
			t.Errorf("companion file produced a BeforePath artifact: %q", op.BeforePath)
		}
	}

	// No artifacts should have been written for companion files.
	if arts := store.Artifacts["run-4"]; len(arts) != 0 {
		t.Errorf("expected no artifacts for companion files, got %d", len(arts))
	}
}

// TestWritePlanWithArtifacts_PathRelativization verifies that PlanOp.Path is
// stored relative to quadletDir using forward slashes.
func TestWritePlanWithArtifacts_PathRelativization(t *testing.T) {
	tmpDir := t.TempDir()
	quadletDir := filepath.Join(tmpDir, "quadlets")
	srcDir := filepath.Join(tmpDir, "src")

	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	srcFile := filepath.Join(srcDir, "web.container")
	if err := os.WriteFile(srcFile, []byte("[Container]\nImage=nginx\n"), 0644); err != nil {
		t.Fatal(err)
	}

	syncPlan := &quadsyncd.Plan{
		Add: []quadsyncd.FileOp{
			{
				SourcePath: srcFile,
				DestPath:   filepath.Join(quadletDir, "web.container"),
			},
		},
		Update: []quadsyncd.FileOp{},
		Delete: []quadsyncd.FileOp{},
	}

	store := testutil.NewMockRunStore()
	logger := testutil.TestLogger()
	ctx := context.Background()

	plan := writePlanWithArtifacts(ctx, store, "run-5", syncPlan, nil, quadletDir, newTestPlanRequest(), logger)

	if len(plan.Ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(plan.Ops))
	}
	// Path must be relative (no leading separator) and use forward slashes.
	p := plan.Ops[0].Path
	if filepath.IsAbs(p) {
		t.Errorf("expected relative path, got absolute: %q", p)
	}
	if p != "web.container" {
		t.Errorf("expected path=web.container, got %q", p)
	}
}
