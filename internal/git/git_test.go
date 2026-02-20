package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initBareRepo creates a bare-like local repo with an initial commit on the given branch.
func initBareRepo(t *testing.T, dir, branch string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init", "-b", branch, dir},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
}

// commitFile creates or overwrites a file and commits it.
func commitFile(t *testing.T, repoDir, content, msg string) {
	t.Helper()
	const name = "hello.container"
	if err := os.WriteFile(filepath.Join(repoDir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "-C", repoDir, "add", name},
		{"git", "-C", repoDir, "commit", "-m", msg},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
}

func TestEnsureCheckout_UpdatesLocalBranch(t *testing.T) {
	ctx := context.Background()

	// Create a "remote" repo with an initial commit.
	remoteDir := t.TempDir()
	initBareRepo(t, remoteDir, "main")
	commitFile(t, remoteDir, "version1\n", "Initial commit")

	// First checkout: clones the repo.
	cloneDir := filepath.Join(t.TempDir(), "repo")
	client := NewShellClient("", "")
	commit1, err := client.EnsureCheckout(ctx, remoteDir, "main", cloneDir)
	if err != nil {
		t.Fatalf("first checkout: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(cloneDir, "hello.container"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "version1\n" {
		t.Fatalf("expected version1, got %q", string(got))
	}

	// Push a new commit to the remote.
	commitFile(t, remoteDir, "version2\n", "Update")

	// Second checkout: must pick up the new commit.
	commit2, err := client.EnsureCheckout(ctx, remoteDir, "main", cloneDir)
	if err != nil {
		t.Fatalf("second checkout: %v", err)
	}
	if commit1 == commit2 {
		t.Error("expected different commit after update, but got the same")
	}

	got, err = os.ReadFile(filepath.Join(cloneDir, "hello.container"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "version2\n" {
		t.Errorf("expected version2 after update, got %q", string(got))
	}
}

func TestEnsureCheckout_TagsStillWork(t *testing.T) {
	ctx := context.Background()

	// Create a remote repo with a tagged commit.
	remoteDir := t.TempDir()
	initBareRepo(t, remoteDir, "main")
	commitFile(t, remoteDir, "tagged\n", "Tagged commit")
	if out, err := exec.Command("git", "-C", remoteDir, "tag", "v1.0").CombinedOutput(); err != nil {
		t.Fatalf("tag: %v: %s", err, out)
	}

	// Add another commit so main moves ahead of the tag.
	commitFile(t, remoteDir, "after-tag\n", "Post-tag commit")

	// Checkout the tag.
	cloneDir := filepath.Join(t.TempDir(), "repo")
	client := NewShellClient("", "")
	_, err := client.EnsureCheckout(ctx, remoteDir, "v1.0", cloneDir)
	if err != nil {
		t.Fatalf("tag checkout: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(cloneDir, "hello.container"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "tagged\n" {
		t.Errorf("expected tagged content, got %q", string(got))
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple path", input: "/home/user/.ssh/key", want: "'/home/user/.ssh/key'"},
		{name: "path with spaces", input: "/home/my user/key", want: "'/home/my user/key'"},
		{name: "path with single quote", input: "/home/user's/key", want: "'/home/user'\\''s/key'"},
		{name: "empty string", input: "", want: "''"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellQuote(tt.input)
			if got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestInsertGitFlags(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		flags []string
		want  []string
	}{
		{
			name:  "insert before subcommand",
			args:  []string{"git", "clone", "--no-checkout", "url", "dest"},
			flags: []string{"-c", "key=value"},
			want:  []string{"git", "-c", "key=value", "clone", "--no-checkout", "url", "dest"},
		},
		{
			name:  "insert before fetch",
			args:  []string{"git", "-C", "/dir", "fetch", "origin"},
			flags: []string{"-c", "cred=helper"},
			want:  []string{"git", "-c", "cred=helper", "-C", "/dir", "fetch", "origin"},
		},
		{
			name:  "empty args",
			args:  []string{},
			flags: []string{"-c", "key=value"},
			want:  []string{"-c", "key=value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := insertGitFlags(tt.args, tt.flags...)
			if len(got) != len(tt.want) {
				t.Fatalf("insertGitFlags() length = %d, want %d\ngot:  %v\nwant: %v", len(got), len(tt.want), got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("insertGitFlags()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
