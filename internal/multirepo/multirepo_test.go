package multirepo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/git"
)

// mockGitClient implements git.Client for testing without network access.
type mockGitClient struct {
	commit    string
	err       error
	repoSetup func(destDir string)
}

func (m *mockGitClient) EnsureCheckout(_ context.Context, _, _, destDir string) (string, error) {
	if m.repoSetup != nil {
		m.repoSetup(destDir)
	}
	return m.commit, m.err
}

// makeSpec is a helper to build a RepoSpec.
func makeSpec(url, ref string, priority int) config.RepoSpec {
	return config.RepoSpec{URL: url, Ref: ref, Priority: priority}
}

// fakeRepoState builds a RepoState directly (without hitting git).
func fakeRepoState(url, ref, commit string, priority int, files map[string]string) RepoState {
	spec := config.RepoSpec{URL: url, Ref: ref, Priority: priority}
	var repoFiles []RepoFile
	for mergeKey, absPath := range files {
		repoFiles = append(repoFiles, RepoFile{MergeKey: mergeKey, AbsPath: absPath})
	}
	return RepoState{Spec: spec, Commit: commit, Files: repoFiles}
}

// ---- normalizeMergeKey ----

func TestNormalizeMergeKey(t *testing.T) {
	tests := []struct {
		name    string
		rel     string
		want    string
		wantErr bool
	}{
		{name: "simple file", rel: "app.container", want: "app.container"},
		{name: "subdir file", rel: "subdir/app.container", want: "subdir/app.container"},
		{name: "dot prefix", rel: "./app.container", want: "app.container"},
		{name: "traversal dotdot", rel: "../etc/passwd", wantErr: true},
		{name: "deep traversal", rel: "a/../../etc/passwd", wantErr: true},
		{name: "absolute path", rel: "/etc/passwd", wantErr: true},
		{name: "windows drive", rel: "C:\\file", wantErr: true},
		{name: "pure dotdot", rel: "..", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeMergeKey(tt.rel)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for rel=%q, got nil", tt.rel)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("normalizeMergeKey(%q) = %q, want %q", tt.rel, got, tt.want)
			}
		})
	}
}

// ---- Merge ----

func TestMerge_DisjointPaths(t *testing.T) {
	states := []RepoState{
		fakeRepoState("https://a.example/repo1", "main", "sha1", 0, map[string]string{
			"app.container": "/repo1/app.container",
		}),
		fakeRepoState("https://b.example/repo2", "main", "sha2", 0, map[string]string{
			"db.container": "/repo2/db.container",
		}),
	}

	result, err := Merge(states, config.ConflictPreferHighestPriority)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Items) != 2 {
		t.Errorf("want 2 items, got %d", len(result.Items))
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("want 0 conflicts, got %d", len(result.Conflicts))
	}
}

func TestMerge_ConflictPreferHighestPriority(t *testing.T) {
	states := []RepoState{
		fakeRepoState("https://lo.example/repo", "main", "sha-lo", 1, map[string]string{
			"app.container": "/lo/app.container",
		}),
		fakeRepoState("https://hi.example/repo", "main", "sha-hi", 10, map[string]string{
			"app.container": "/hi/app.container",
		}),
	}

	result, err := Merge(states, config.ConflictPreferHighestPriority)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(result.Items))
	}
	if result.Items[0].SourceRepo != "https://hi.example/repo" {
		t.Errorf("winner = %q, want hi repo", result.Items[0].SourceRepo)
	}
	if len(result.Conflicts) != 1 {
		t.Errorf("want 1 conflict record, got %d", len(result.Conflicts))
	}
}

func TestMerge_ConflictTieBreakByConfigOrder(t *testing.T) {
	// Same priority → first in the list wins.
	states := []RepoState{
		fakeRepoState("https://first.example/repo", "main", "sha-first", 5, map[string]string{
			"app.container": "/first/app.container",
		}),
		fakeRepoState("https://second.example/repo", "main", "sha-second", 5, map[string]string{
			"app.container": "/second/app.container",
		}),
	}

	result, err := Merge(states, config.ConflictPreferHighestPriority)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(result.Items))
	}
	if result.Items[0].SourceRepo != "https://first.example/repo" {
		t.Errorf("winner = %q, want first repo", result.Items[0].SourceRepo)
	}
}

func TestMerge_ConflictFail_ReturnsAllConflicts(t *testing.T) {
	states := []RepoState{
		fakeRepoState("https://a.example/repo", "main", "sha-a", 0, map[string]string{
			"app.container": "/a/app.container",
			"db.container":  "/a/db.container",
		}),
		fakeRepoState("https://b.example/repo", "main", "sha-b", 0, map[string]string{
			"app.container": "/b/app.container",
			"db.container":  "/b/db.container",
		}),
	}

	_, err := Merge(states, config.ConflictFail)
	if err == nil {
		t.Fatal("expected error for conflicts in fail mode, got nil")
	}
	// Error must mention both conflicting paths.
	for _, path := range []string{"app.container", "db.container"} {
		if !containsStr(err.Error(), path) {
			t.Errorf("error %q should mention %q", err.Error(), path)
		}
	}
}

func TestMerge_UnitNameCollision_AlwaysFails(t *testing.T) {
	// Two different paths produce the same unit name:
	// "a/app.container" → "app.service"
	// "b/app.container" → "app.service"
	states := []RepoState{
		fakeRepoState("https://a.example/repo", "main", "sha-a", 0, map[string]string{
			"a/app.container": "/checkout/a/app.container",
		}),
		fakeRepoState("https://b.example/repo", "main", "sha-b", 0, map[string]string{
			"b/app.container": "/checkout/b/app.container",
		}),
	}

	_, err := Merge(states, config.ConflictPreferHighestPriority)
	if err == nil {
		t.Fatal("expected unit-name collision error, got nil")
	}
	if !containsStr(err.Error(), "unit-name collision") {
		t.Errorf("error %q should mention unit-name collision", err.Error())
	}
}

func TestMerge_EmptyStates(t *testing.T) {
	result, err := Merge([]RepoState{}, config.ConflictPreferHighestPriority)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("want 0 items, got %d", len(result.Items))
	}
}

// ---- LoadRepoState ----

func TestLoadRepoState_Success(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	srcDir := repoDir

	gitMock := &mockGitClient{
		commit: "abc123",
		repoSetup: func(destDir string) {
			_ = os.MkdirAll(destDir, 0755)
			_ = os.WriteFile(filepath.Join(destDir, "app.container"), []byte("[Container]\n"), 0644)
			_ = os.WriteFile(filepath.Join(destDir, "app.env"), []byte("FOO=bar\n"), 0644)
		},
	}

	spec := makeSpec("https://example.com/repo", "main", 0)
	rs, err := LoadRepoState(context.Background(), spec, repoDir, srcDir, gitMock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rs.Commit != "abc123" {
		t.Errorf("commit = %q, want abc123", rs.Commit)
	}
	if len(rs.Files) != 2 {
		t.Errorf("want 2 files, got %d", len(rs.Files))
	}
}

func TestLoadRepoState_GitError(t *testing.T) {
	tmpDir := t.TempDir()
	gitErr := errors.New("clone failed")
	gitMock := &mockGitClient{err: gitErr}
	spec := makeSpec("https://example.com/repo", "main", 0)

	_, err := LoadRepoState(context.Background(), spec, filepath.Join(tmpDir, "repo"), tmpDir, gitMock)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, gitErr) {
		t.Errorf("error should wrap git error: %v", err)
	}
}

func TestLoadRepoState_RejectsSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	_ = os.MkdirAll(repoDir, 0755)

	// Create a regular file and a symlink to it.
	target := filepath.Join(repoDir, "real.container")
	link := filepath.Join(repoDir, "link.container")
	_ = os.WriteFile(target, []byte("[Container]\n"), 0644)
	if err := os.Symlink(target, link); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}

	gitMock := &mockGitClient{commit: "abc"}
	spec := makeSpec("https://example.com/repo", "main", 0)

	_, err := LoadRepoState(context.Background(), spec, repoDir, repoDir, gitMock)
	if err == nil {
		t.Fatal("expected error for symlink, got nil")
	}
	if !containsStr(err.Error(), "symlink") {
		t.Errorf("error %q should mention symlink", err.Error())
	}
}

func TestLoadRepoState_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	_ = os.MkdirAll(repoDir, 0755)

	gitMock := &mockGitClient{commit: "abc", repoSetup: func(_ string) {}}
	spec := makeSpec("https://example.com/repo", "main", 0)

	rs, err := LoadRepoState(context.Background(), spec, repoDir, repoDir, gitMock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rs.Files) != 0 {
		t.Errorf("want 0 files, got %d", len(rs.Files))
	}
}

// ---- helpers ----

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

// Ensure the interface is satisfied at compile time.
var _ git.Client = (*mockGitClient)(nil)
