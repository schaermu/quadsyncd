package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/schaermu/quadsyncd/internal/config"
)

// mockGitClient implements git.Client for testing.
type mockGitClient struct {
	commitHash string
	err        error
	called     bool
	repoSetup  func(destDir string)
}

func (m *mockGitClient) EnsureCheckout(_ context.Context, _, _, destDir string) (string, error) {
	m.called = true
	if m.repoSetup != nil {
		m.repoSetup(destDir)
	}
	return m.commitHash, m.err
}

// mockSystemd implements systemduser.Systemd for testing.
type mockSystemd struct {
	available      bool
	availableErr   error
	reloadErr      error
	restartErr     error
	validateErr    error
	reloadCalled   bool
	restartCalled  bool
	validateCalled bool
	restartedUnits []string
}

func (m *mockSystemd) IsAvailable(_ context.Context) (bool, error) {
	return m.available, m.availableErr
}

func (m *mockSystemd) DaemonReload(_ context.Context) error {
	m.reloadCalled = true
	return m.reloadErr
}

func (m *mockSystemd) TryRestartUnits(_ context.Context, units []string) error {
	m.restartCalled = true
	m.restartedUnits = units
	return m.restartErr
}

func (m *mockSystemd) ValidateQuadlets(_ context.Context) error {
	m.validateCalled = true
	return m.validateErr
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

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

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "src.txt")
	dstPath := filepath.Join(tmpDir, "sub", "dst.txt")

	content := []byte("hello world")
	if err := os.WriteFile(srcPath, content, 0755); err != nil {
		t.Fatal(err)
	}

	engine := &Engine{logger: testLogger()}
	if err := engine.copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile: %v", err)
	}

	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}

	srcInfo, _ := os.Stat(srcPath)
	dstInfo, _ := os.Stat(dstPath)
	if srcInfo.Mode() != dstInfo.Mode() {
		t.Errorf("permission mismatch: src %v, dst %v", srcInfo.Mode(), dstInfo.Mode())
	}
}

func TestCopyFile_NonExistentSource(t *testing.T) {
	tmpDir := t.TempDir()
	engine := &Engine{logger: testLogger()}
	err := engine.copyFile(filepath.Join(tmpDir, "no-such-file"), filepath.Join(tmpDir, "dst"))
	if err == nil {
		t.Fatal("expected error for non-existent source")
	}
}

func TestApplyPlan(t *testing.T) {
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	quadletDir := filepath.Join(tmpDir, "quadlet")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(quadletDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create source files for add and update
	addSrc := filepath.Join(srcDir, "new.container")
	updateSrc := filepath.Join(srcDir, "upd.container")
	if err := os.WriteFile(addSrc, []byte("add-content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(updateSrc, []byte("upd-content"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a file to be deleted
	delDst := filepath.Join(quadletDir, "old.container")
	if err := os.WriteFile(delDst, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Paths: config.PathsConfig{QuadletDir: quadletDir},
	}
	engine := &Engine{cfg: cfg, logger: testLogger()}

	plan := &Plan{
		Add:    []FileOp{{SourcePath: addSrc, DestPath: filepath.Join(quadletDir, "new.container")}},
		Update: []FileOp{{SourcePath: updateSrc, DestPath: filepath.Join(quadletDir, "upd.container")}},
		Delete: []FileOp{{DestPath: delDst}},
	}

	if err := engine.applyPlan(plan); err != nil {
		t.Fatalf("applyPlan: %v", err)
	}

	// Verify add
	if data, err := os.ReadFile(filepath.Join(quadletDir, "new.container")); err != nil || string(data) != "add-content" {
		t.Errorf("add file: err=%v, data=%q", err, data)
	}
	// Verify update
	if data, err := os.ReadFile(filepath.Join(quadletDir, "upd.container")); err != nil || string(data) != "upd-content" {
		t.Errorf("update file: err=%v, data=%q", err, data)
	}
	// Verify delete
	if _, err := os.Stat(delDst); !os.IsNotExist(err) {
		t.Error("deleted file still exists")
	}
}

func TestApplyPlan_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	quadletDir := filepath.Join(tmpDir, "quadlet")
	if err := os.MkdirAll(quadletDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a file that exists
	existing := filepath.Join(quadletDir, "exists.container")
	if err := os.WriteFile(existing, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Paths: config.PathsConfig{QuadletDir: quadletDir},
	}
	engine := &Engine{cfg: cfg, logger: testLogger()}

	plan := &Plan{
		Add:    []FileOp{},
		Update: []FileOp{},
		Delete: []FileOp{
			{DestPath: existing},
			{DestPath: filepath.Join(quadletDir, "nonexistent.container")},
		},
	}

	if err := engine.applyPlan(plan); err != nil {
		t.Fatalf("applyPlan delete: %v", err)
	}

	if _, err := os.Stat(existing); !os.IsNotExist(err) {
		t.Error("existing file should have been deleted")
	}
}

func TestHandleRestarts(t *testing.T) {
	plan := &Plan{
		Add:    []FileOp{{DestPath: "/q/app.container", Hash: "a"}},
		Update: []FileOp{{DestPath: "/q/app.env"}},
		Delete: []FileOp{},
	}

	state := &State{
		Commit: "abc",
		ManagedFiles: map[string]ManagedFile{
			"/q/app.container": {Hash: "a"},
			"/q/db.container":  {Hash: "b"},
			"/q/app.env":       {Hash: "c"},
		},
	}

	tests := []struct {
		name           string
		restart        config.RestartPolicy
		wantRestart    bool
		wantUnitsCount int
		wantErr        bool
	}{
		{
			name:        "none",
			restart:     config.RestartNone,
			wantRestart: false,
		},
		{
			name:           "changed",
			restart:        config.RestartChanged,
			wantRestart:    true,
			wantUnitsCount: 1, // only app.container → app.service; app.env is companion
		},
		{
			name:           "all-managed",
			restart:        config.RestartAllManaged,
			wantRestart:    true,
			wantUnitsCount: 2, // app.service + db.service
		},
		{
			name:    "unknown",
			restart: config.RestartPolicy("bogus"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sd := &mockSystemd{available: true}
			cfg := &config.Config{
				Sync: config.SyncConfig{Restart: tc.restart},
			}
			engine := &Engine{cfg: cfg, systemd: sd, logger: testLogger()}

			err := engine.handleRestarts(context.Background(), plan, state)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantRestart && !sd.restartCalled {
				t.Error("expected TryRestartUnits to be called")
			}
			if !tc.wantRestart && sd.restartCalled {
				t.Error("TryRestartUnits should not be called")
			}
			if tc.wantRestart && len(sd.restartedUnits) != tc.wantUnitsCount {
				t.Errorf("restarted %d units, want %d: %v", len(sd.restartedUnits), tc.wantUnitsCount, sd.restartedUnits)
			}
		})
	}
}

func TestAffectedUnits(t *testing.T) {
	engine := &Engine{logger: testLogger()}
	plan := &Plan{
		Add:    []FileOp{{DestPath: "/q/app.container"}},
		Update: []FileOp{{DestPath: "/q/db.volume"}, {DestPath: "/q/app.env"}},
		Delete: []FileOp{{DestPath: "/q/old.network"}},
	}

	units := engine.affectedUnits(plan)

	want := map[string]bool{"app.service": true, "db-volume.service": true, "old-network.service": true}
	if len(units) != len(want) {
		t.Fatalf("got %d units, want %d: %v", len(units), len(want), units)
	}
	for _, u := range units {
		if !want[u] {
			t.Errorf("unexpected unit %q", u)
		}
	}
}

func TestBuildState(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	repoDir := filepath.Join(stateDir, "repo")

	cfg := &config.Config{
		Repo:  config.RepoConfig{Subdir: ""},
		Paths: config.PathsConfig{QuadletDir: filepath.Join(tmpDir, "q"), StateDir: stateDir},
	}
	engine := &Engine{cfg: cfg, logger: testLogger()}

	prevState := &State{
		Commit: "old",
		ManagedFiles: map[string]ManagedFile{
			"/q/keep.container": {SourcePath: "keep.container", Hash: "aaa"},
			"/q/del.container":  {SourcePath: "del.container", Hash: "bbb"},
		},
	}

	plan := &Plan{
		Add:    []FileOp{{SourcePath: filepath.Join(repoDir, "new.container"), DestPath: "/q/new.container", Hash: "ccc"}},
		Update: []FileOp{{SourcePath: filepath.Join(repoDir, "keep.container"), DestPath: "/q/keep.container", Hash: "aaa-new"}},
		Delete: []FileOp{{DestPath: "/q/del.container"}},
	}

	state := engine.buildState(prevState, plan, "newcommit")

	if state.Commit != "newcommit" {
		t.Errorf("commit = %q, want %q", state.Commit, "newcommit")
	}
	if _, ok := state.ManagedFiles["/q/del.container"]; ok {
		t.Error("deleted file should not be in state")
	}
	if _, ok := state.ManagedFiles["/q/new.container"]; !ok {
		t.Error("added file should be in state")
	}
	if mf, ok := state.ManagedFiles["/q/keep.container"]; !ok || mf.Hash != "aaa-new" {
		t.Errorf("updated file hash = %q, want %q", mf.Hash, "aaa-new")
	}
}

func TestLoadState_NonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Paths: config.PathsConfig{StateDir: tmpDir},
	}
	engine := &Engine{cfg: cfg, logger: testLogger()}

	state, err := engine.loadState()
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if state == nil || state.ManagedFiles == nil {
		t.Fatal("expected empty state, got nil")
	}
	if len(state.ManagedFiles) != 0 {
		t.Errorf("expected 0 managed files, got %d", len(state.ManagedFiles))
	}
}

func TestSaveAndLoadState(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Paths: config.PathsConfig{StateDir: tmpDir},
	}
	engine := &Engine{cfg: cfg, logger: testLogger()}

	original := &State{
		Commit: "abc123",
		ManagedFiles: map[string]ManagedFile{
			"/q/app.container": {SourcePath: "app.container", Hash: "hash1"},
		},
	}

	if err := engine.saveState(original); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	loaded, err := engine.loadState()
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}

	if loaded.Commit != original.Commit {
		t.Errorf("commit: got %q, want %q", loaded.Commit, original.Commit)
	}
	if len(loaded.ManagedFiles) != len(original.ManagedFiles) {
		t.Fatalf("managed files count: got %d, want %d", len(loaded.ManagedFiles), len(original.ManagedFiles))
	}
	for k, v := range original.ManagedFiles {
		got, ok := loaded.ManagedFiles[k]
		if !ok {
			t.Errorf("missing key %q", k)
			continue
		}
		if got != v {
			t.Errorf("key %q: got %+v, want %+v", k, got, v)
		}
	}
}

func TestRun_DryRun(t *testing.T) {
	tmpDir := t.TempDir()
	quadletDir := filepath.Join(tmpDir, "quadlet")
	stateDir := filepath.Join(tmpDir, "state")

	gitMock := &mockGitClient{
		commitHash: "abc",
		repoSetup: func(destDir string) {
			_ = os.MkdirAll(destDir, 0755)
			_ = os.WriteFile(filepath.Join(destDir, "app.container"), []byte("[Container]\nImage=alpine\n"), 0644)
		},
	}
	sd := &mockSystemd{available: true}

	cfg := &config.Config{
		Repo:  config.RepoConfig{URL: "file:///test", Ref: "main"},
		Paths: config.PathsConfig{QuadletDir: quadletDir, StateDir: stateDir},
		Sync:  config.SyncConfig{Prune: true, Restart: config.RestartChanged},
	}

	engine := NewEngine(cfg, gitMock, sd, testLogger(), true)

	if err := engine.Run(context.Background()); err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}

	if !gitMock.called {
		t.Error("git should be called in dry-run")
	}
	if sd.reloadCalled {
		t.Error("systemd reload should NOT be called in dry-run")
	}
	if sd.restartCalled {
		t.Error("systemd restart should NOT be called in dry-run")
	}
	// Files should not be copied
	if _, err := os.Stat(filepath.Join(quadletDir, "app.container")); !os.IsNotExist(err) {
		t.Error("quadlet file should not exist in dry-run")
	}
}

func TestRun_FullSync(t *testing.T) {
	tmpDir := t.TempDir()
	quadletDir := filepath.Join(tmpDir, "quadlet")
	stateDir := filepath.Join(tmpDir, "state")

	gitMock := &mockGitClient{
		commitHash: "def456",
		repoSetup: func(destDir string) {
			_ = os.MkdirAll(destDir, 0755)
			_ = os.WriteFile(filepath.Join(destDir, "web.container"), []byte("[Container]\nImage=nginx\n"), 0644)
		},
	}
	sd := &mockSystemd{available: true}

	cfg := &config.Config{
		Repo:  config.RepoConfig{URL: "file:///test", Ref: "main"},
		Paths: config.PathsConfig{QuadletDir: quadletDir, StateDir: stateDir},
		Sync:  config.SyncConfig{Prune: true, Restart: config.RestartChanged},
	}

	engine := NewEngine(cfg, gitMock, sd, testLogger(), false)

	if err := engine.Run(context.Background()); err != nil {
		t.Fatalf("Run full sync: %v", err)
	}

	// File should be copied
	data, err := os.ReadFile(filepath.Join(quadletDir, "web.container"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(data) != "[Container]\nImage=nginx\n" {
		t.Errorf("file content mismatch: %q", data)
	}

	// State file should exist
	if _, err := os.Stat(cfg.StateFilePath()); err != nil {
		t.Errorf("state file not saved: %v", err)
	}

	if !sd.reloadCalled {
		t.Error("systemd reload should be called")
	}
	if !sd.restartCalled {
		t.Error("systemd restart should be called for changed units")
	}
}

func TestRun_GitError(t *testing.T) {
	tmpDir := t.TempDir()
	gitMock := &mockGitClient{err: errors.New("clone failed")}
	sd := &mockSystemd{available: true}

	cfg := &config.Config{
		Repo:  config.RepoConfig{URL: "file:///test", Ref: "main"},
		Paths: config.PathsConfig{QuadletDir: filepath.Join(tmpDir, "q"), StateDir: filepath.Join(tmpDir, "s")},
		Sync:  config.SyncConfig{Restart: config.RestartChanged},
	}

	engine := NewEngine(cfg, gitMock, sd, testLogger(), false)
	err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from git failure")
	}
	if !errors.Is(err, gitMock.err) {
		t.Errorf("error should wrap git error: %v", err)
	}
}

func TestRun_SystemdUnavailable(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")

	gitMock := &mockGitClient{
		commitHash: "abc",
		repoSetup: func(destDir string) {
			_ = os.MkdirAll(destDir, 0755)
			_ = os.WriteFile(filepath.Join(destDir, "x.container"), []byte("c"), 0644)
		},
	}
	sd := &mockSystemd{available: false}

	cfg := &config.Config{
		Repo:  config.RepoConfig{URL: "file:///test", Ref: "main"},
		Paths: config.PathsConfig{QuadletDir: filepath.Join(tmpDir, "q"), StateDir: stateDir},
		Sync:  config.SyncConfig{Restart: config.RestartChanged},
	}

	engine := NewEngine(cfg, gitMock, sd, testLogger(), false)
	err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when systemd unavailable")
	}
}

func TestLogPlanDetails(t *testing.T) {
	engine := &Engine{logger: testLogger()}
	plan := &Plan{
		Add:    []FileOp{{SourcePath: "/src/a.container", DestPath: "/dst/a.container"}},
		Update: []FileOp{{SourcePath: "/src/b.container", DestPath: "/dst/b.container"}},
		Delete: []FileOp{{DestPath: "/dst/c.container"}},
	}
	// Should not panic
	engine.logPlanDetails(plan)
}

func TestBuildPlan_UpdateAndDelete(t *testing.T) {
	tmpDir := t.TempDir()
	quadletDir := filepath.Join(tmpDir, "quadlet")
	stateDir := filepath.Join(tmpDir, "state")
	repoDir := filepath.Join(stateDir, "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write one changed file and omit the other (to trigger delete)
	changedContent := []byte("updated content")
	if err := os.WriteFile(filepath.Join(repoDir, "app.container"), changedContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Compute hash manually for the old file
	oldHash := "oldhash"
	// Compute hash for the new file by writing it
	newHash, err := fileHash(filepath.Join(repoDir, "app.container"))
	if err != nil {
		t.Fatal(err)
	}

	prevState := &State{
		Commit: "old",
		ManagedFiles: map[string]ManagedFile{
			filepath.Join(quadletDir, "app.container"):    {SourcePath: "app.container", Hash: oldHash},
			filepath.Join(quadletDir, "remove.container"): {SourcePath: "remove.container", Hash: "removehash"},
		},
	}

	cfg := &config.Config{
		Repo:  config.RepoConfig{Subdir: ""},
		Paths: config.PathsConfig{QuadletDir: quadletDir, StateDir: stateDir},
		Sync:  config.SyncConfig{Prune: true, Restart: config.RestartChanged},
	}

	engine := &Engine{cfg: cfg, logger: testLogger()}

	plan, err := engine.buildPlan(prevState)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}

	// app.container should be updated (hash differs)
	if len(plan.Update) != 1 {
		t.Errorf("expected 1 update, got %d", len(plan.Update))
	} else {
		if plan.Update[0].Hash != newHash {
			t.Errorf("update hash = %q, want %q", plan.Update[0].Hash, newHash)
		}
	}

	// remove.container should be deleted (not in repo)
	if len(plan.Delete) != 1 {
		t.Errorf("expected 1 delete, got %d", len(plan.Delete))
	} else {
		if filepath.Base(plan.Delete[0].DestPath) != "remove.container" {
			t.Errorf("delete file = %q, want remove.container", plan.Delete[0].DestPath)
		}
	}

	// No adds
	if len(plan.Add) != 0 {
		t.Errorf("expected 0 adds, got %d", len(plan.Add))
	}
}

func TestLoadState_CorruptedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths: config.PathsConfig{StateDir: stateDir},
	}
	engine := &Engine{cfg: cfg, logger: testLogger()}
	// Write invalid JSON
	if err := os.WriteFile(cfg.StateFilePath(), []byte("{invalid json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := engine.loadState()
	if err == nil {
		t.Error("expected error for corrupted JSON, got nil")
	}
}

func TestHandleRestarts_ChangedNoQuadletChanges(t *testing.T) {
	ms := &mockSystemd{available: true}
	cfg := &config.Config{
		Sync: config.SyncConfig{Restart: config.RestartChanged},
	}
	engine := &Engine{cfg: cfg, systemd: ms, logger: testLogger()}
	plan := &Plan{
		Add: []FileOp{{DestPath: "/quadlet/myapp.env", SourcePath: "/src/myapp.env"}},
	}
	state := &State{ManagedFiles: map[string]ManagedFile{}}
	err := engine.handleRestarts(context.Background(), plan, state)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if ms.restartCalled {
		t.Error("TryRestartUnits should not be called when there are no quadlet changes")
	}
}

func TestHandleRestarts_AllManagedNoQuadletFiles(t *testing.T) {
	ms := &mockSystemd{available: true}
	cfg := &config.Config{
		Sync: config.SyncConfig{Restart: config.RestartAllManaged},
	}
	engine := &Engine{cfg: cfg, systemd: ms, logger: testLogger()}
	plan := &Plan{}
	state := &State{
		ManagedFiles: map[string]ManagedFile{
			"/quadlet/app.env": {SourcePath: "app.env", Hash: "abc"},
		},
	}
	err := engine.handleRestarts(context.Background(), plan, state)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if ms.restartCalled {
		t.Error("TryRestartUnits should not be called when there are no quadlet files")
	}
}

// TestRun_RecoversFromCorruptedState verifies that the sync engine treats a
// corrupted state file as a fresh sync rather than a fatal error.
func TestRun_RecoversFromCorruptedState(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	quadletDir := filepath.Join(tmpDir, "quadlet")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Repo:  config.RepoConfig{URL: "file:///test", Ref: "main"},
		Paths: config.PathsConfig{QuadletDir: quadletDir, StateDir: stateDir},
		Sync:  config.SyncConfig{Prune: false, Restart: config.RestartNone},
	}
	// Write corrupted state file
	stateFile := filepath.Join(stateDir, "state.json")
	if err := os.WriteFile(stateFile, []byte("{corrupted"), 0644); err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(stateDir, "repo")
	mg := &mockGitClient{
		commitHash: "abc123",
		repoSetup: func(destDir string) {
			_ = os.MkdirAll(destDir, 0755)
			_ = os.WriteFile(filepath.Join(destDir, "app.container"), []byte("[Container]"), 0644)
		},
	}
	ms := &mockSystemd{available: true}
	engine := NewEngine(cfg, mg, ms, testLogger(), false)
	// Create repo dir so git mock can use it
	_ = os.MkdirAll(repoDir, 0755)
	err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("Run should recover from corrupted state, got error: %v", err)
	}
}

// TestRun_HandleRestartsError verifies that restart failures are treated as
// non-fatal warnings (the sync still succeeds). This is by design: the files
// have already been synced and the daemon reloaded, so a restart failure should
// not roll back or report the entire sync as failed.
func TestRun_HandleRestartsError(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	quadletDir := filepath.Join(tmpDir, "quadlet")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Repo:  config.RepoConfig{URL: "file:///test", Ref: "main"},
		Paths: config.PathsConfig{QuadletDir: quadletDir, StateDir: stateDir},
		Sync:  config.SyncConfig{Prune: false, Restart: config.RestartChanged},
	}
	repoDir := filepath.Join(stateDir, "repo")
	mg := &mockGitClient{
		commitHash: "abc123",
		repoSetup: func(destDir string) {
			_ = os.MkdirAll(destDir, 0755)
			_ = os.WriteFile(filepath.Join(destDir, "app.container"), []byte("[Container]"), 0644)
		},
	}
	ms := &mockSystemd{available: true, restartErr: fmt.Errorf("restart failed")}
	engine := NewEngine(cfg, mg, ms, testLogger(), false)
	_ = os.MkdirAll(repoDir, 0755)
	err := engine.Run(context.Background())
	if err != nil {
		t.Errorf("Run should not fail due to restart error, got: %v", err)
	}
}

func TestRun_DaemonReloadError(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	quadletDir := filepath.Join(tmpDir, "quadlet")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Repo:  config.RepoConfig{URL: "file:///test", Ref: "main"},
		Paths: config.PathsConfig{QuadletDir: quadletDir, StateDir: stateDir},
		Sync:  config.SyncConfig{Prune: false, Restart: config.RestartNone},
	}
	repoDir := filepath.Join(stateDir, "repo")
	mg := &mockGitClient{
		commitHash: "abc123",
		repoSetup: func(destDir string) {
			_ = os.MkdirAll(destDir, 0755)
			_ = os.WriteFile(filepath.Join(destDir, "app.container"), []byte("[Container]"), 0644)
		},
	}
	ms := &mockSystemd{available: true, reloadErr: fmt.Errorf("daemon-reload failed")}
	engine := NewEngine(cfg, mg, ms, testLogger(), false)
	_ = os.MkdirAll(repoDir, 0755)
	err := engine.Run(context.Background())
	if err == nil {
		t.Error("expected error when DaemonReload fails, got nil")
	}
}

func TestRun_BuildPlanError(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Repo:  config.RepoConfig{URL: "file:///test", Ref: "main", Subdir: "nonexistent-subdir"},
		Paths: config.PathsConfig{QuadletDir: filepath.Join(tmpDir, "quadlet"), StateDir: stateDir},
		Sync:  config.SyncConfig{Prune: false, Restart: config.RestartNone},
	}
	mg := &mockGitClient{
		commitHash: "abc123",
		repoSetup: func(destDir string) {
			// Create repo dir but NOT the subdir, so DiscoverAllFiles will fail
			_ = os.MkdirAll(destDir, 0755)
		},
	}
	ms := &mockSystemd{available: true}
	engine := NewEngine(cfg, mg, ms, testLogger(), false)
	err := engine.Run(context.Background())
	if err == nil {
		t.Error("expected error when buildPlan fails, got nil")
	}
}

func TestRun_SaveStateError(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	quadletDir := filepath.Join(tmpDir, "quadlet")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Repo:  config.RepoConfig{URL: "file:///test", Ref: "main"},
		Paths: config.PathsConfig{QuadletDir: quadletDir, StateDir: stateDir},
		Sync:  config.SyncConfig{Prune: false, Restart: config.RestartNone},
	}
	repoDir := filepath.Join(stateDir, "repo")
	mg := &mockGitClient{
		commitHash: "abc123",
		repoSetup: func(destDir string) {
			_ = os.MkdirAll(destDir, 0755)
			_ = os.WriteFile(filepath.Join(destDir, "app.container"), []byte("[Container]"), 0644)
		},
	}
	ms := &mockSystemd{available: true}
	engine := NewEngine(cfg, mg, ms, testLogger(), false)
	_ = os.MkdirAll(repoDir, 0755)
	// Point the state file at a path whose parent is a regular file, not a
	// directory. This deterministically prevents writing regardless of the
	// user's privileges (including root), unlike a read-only chmod approach.
	blocker := filepath.Join(stateDir, "state.json")
	if err := os.MkdirAll(blocker, 0755); err != nil {
		t.Fatal(err)
	}
	err := engine.Run(context.Background())
	if err == nil {
		t.Error("expected error when saveState fails, got nil")
	}
}

func TestFileHash_NonExistentFile(t *testing.T) {
	_, err := fileHash("/nonexistent/file.txt")
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

func TestRun_ValidateQuadletsError(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	quadletDir := filepath.Join(tmpDir, "quadlet")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Repo:  config.RepoConfig{URL: "file:///test", Ref: "main"},
		Paths: config.PathsConfig{QuadletDir: quadletDir, StateDir: stateDir},
		Sync:  config.SyncConfig{Prune: false, Restart: config.RestartNone},
	}
	mg := &mockGitClient{
		commitHash: "abc123",
		repoSetup: func(destDir string) {
			_ = os.MkdirAll(destDir, 0755)
			_ = os.WriteFile(filepath.Join(destDir, "app.container"), []byte("[Container]"), 0644)
		},
	}
	ms := &mockSystemd{available: true, validateErr: fmt.Errorf("invalid quadlet syntax")}
	engine := NewEngine(cfg, mg, ms, testLogger(), false)
	err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when ValidateQuadlets fails, got nil")
	}
	if !ms.validateCalled {
		t.Error("ValidateQuadlets should have been called")
	}
	// Sync should fail before daemon-reload when validation fails
	if ms.reloadCalled {
		t.Error("DaemonReload should not be called when validation fails")
	}
}

func TestRun_ValidateQuadletsCalled(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	quadletDir := filepath.Join(tmpDir, "quadlet")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Repo:  config.RepoConfig{URL: "file:///test", Ref: "main"},
		Paths: config.PathsConfig{QuadletDir: quadletDir, StateDir: stateDir},
		Sync:  config.SyncConfig{Prune: false, Restart: config.RestartNone},
	}
	mg := &mockGitClient{
		commitHash: "abc123",
		repoSetup: func(destDir string) {
			_ = os.MkdirAll(destDir, 0755)
			_ = os.WriteFile(filepath.Join(destDir, "app.container"), []byte("[Container]"), 0644)
		},
	}
	ms := &mockSystemd{available: true}
	engine := NewEngine(cfg, mg, ms, testLogger(), false)
	if err := engine.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !ms.validateCalled {
		t.Error("ValidateQuadlets should be called during a full sync")
	}
	if !ms.reloadCalled {
		t.Error("DaemonReload should be called after successful validation")
	}
}
