package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// envContains reports whether the env slice contains a variable with the given prefix.
func envContains(env []string, prefix string) bool {
	for _, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// envValue returns the value portion of a "KEY=VALUE" entry matching the given key.
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			return e[len(prefix):], true
		}
	}
	return "", false
}

func TestConfigureAuth_SSH(t *testing.T) {
	client := &ShellClient{sshKeyFile: "/tmp/test-key"}
	cmd := exec.Command("git", "clone", "git@github.com:user/repo.git", "/dest")

	if err := client.configureAuth(cmd, "git@github.com:user/repo.git"); err != nil {
		t.Fatalf("configureAuth() error = %v", err)
	}

	val, ok := envValue(cmd.Env, "GIT_SSH_COMMAND")
	if !ok {
		t.Fatal("expected GIT_SSH_COMMAND to be set")
	}
	if !strings.Contains(val, "/tmp/test-key") {
		t.Errorf("GIT_SSH_COMMAND = %q, want it to contain key path", val)
	}
}

func TestConfigureAuth_HTTPS(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("example-token-value\n"), 0600); err != nil {
		t.Fatal(err)
	}

	client := &ShellClient{httpsTokenFile: tokenFile}
	cmd := exec.Command("git", "clone", "https://github.com/user/repo.git", "/dest")

	if err := client.configureAuth(cmd, "https://github.com/user/repo.git"); err != nil {
		t.Fatalf("configureAuth() error = %v", err)
	}

	if !envContains(cmd.Env, "GIT_TERMINAL_PROMPT=0") {
		t.Error("expected GIT_TERMINAL_PROMPT=0 in env")
	}

	tokenVal, ok := envValue(cmd.Env, "QUADSYNCD_GIT_TOKEN")
	if !ok {
		t.Fatal("expected QUADSYNCD_GIT_TOKEN in env")
	}
	if tokenVal != "example-token-value" {
		t.Errorf("QUADSYNCD_GIT_TOKEN = %q, want %q", tokenVal, "example-token-value")
	}

	// Verify git flags were inserted: cmd.Args should contain "-c" and a credential.helper value.
	foundCredHelper := false
	for i, a := range cmd.Args {
		if a == "-c" && i+1 < len(cmd.Args) && strings.HasPrefix(cmd.Args[i+1], "credential.helper=") {
			foundCredHelper = true
			break
		}
	}
	if !foundCredHelper {
		t.Errorf("expected -c credential.helper=... in cmd.Args, got %v", cmd.Args)
	}
}

func TestConfigureAuth_NoAuth(t *testing.T) {
	client := &ShellClient{}
	cmd := exec.Command("git", "clone", "https://github.com/user/repo.git", "/dest")

	if err := client.configureAuth(cmd, "https://github.com/user/repo.git"); err != nil {
		t.Fatalf("configureAuth() error = %v", err)
	}

	// No special auth vars should be added beyond the inherited environment.
	if envContains(cmd.Env, "GIT_SSH_COMMAND=") {
		t.Error("GIT_SSH_COMMAND should not be set")
	}
	if envContains(cmd.Env, "QUADSYNCD_GIT_TOKEN=") {
		t.Error("QUADSYNCD_GIT_TOKEN should not be set")
	}
	if envContains(cmd.Env, "GIT_TERMINAL_PROMPT=") {
		t.Error("GIT_TERMINAL_PROMPT should not be set")
	}
}

func TestConfigureAuth_HTTPSTokenReadError(t *testing.T) {
	client := &ShellClient{httpsTokenFile: filepath.Join(t.TempDir(), "nonexistent")}
	cmd := exec.Command("git", "clone", "https://github.com/user/repo.git", "/dest")

	err := client.configureAuth(cmd, "https://github.com/user/repo.git")
	if err == nil {
		t.Fatal("expected error when token file does not exist")
	}
	if !strings.Contains(err.Error(), "HTTPS token file") {
		t.Errorf("error = %q, want it to mention HTTPS token file", err)
	}
}

func TestConfigureAuth_SSHWithHTTPSURL(t *testing.T) {
	client := &ShellClient{sshKeyFile: "/tmp/test-key"}
	cmd := exec.Command("git", "clone", "https://github.com/user/repo.git", "/dest")

	if err := client.configureAuth(cmd, "https://github.com/user/repo.git"); err != nil {
		t.Fatalf("configureAuth() error = %v", err)
	}

	if envContains(cmd.Env, "GIT_SSH_COMMAND=") {
		t.Error("GIT_SSH_COMMAND should not be set for HTTPS URL with SSH-only auth")
	}
}

func TestConfigureAuth_HTTPSWithSSHURL(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("example-token-value\n"), 0600); err != nil {
		t.Fatal(err)
	}

	client := &ShellClient{httpsTokenFile: tokenFile}
	cmd := exec.Command("git", "clone", "git@github.com:user/repo.git", "/dest")

	if err := client.configureAuth(cmd, "git@github.com:user/repo.git"); err != nil {
		t.Fatalf("configureAuth() error = %v", err)
	}

	if envContains(cmd.Env, "QUADSYNCD_GIT_TOKEN=") {
		t.Error("QUADSYNCD_GIT_TOKEN should not be set for SSH URL with HTTPS-only auth")
	}
	if envContains(cmd.Env, "GIT_TERMINAL_PROMPT=") {
		t.Error("GIT_TERMINAL_PROMPT should not be set for SSH URL with HTTPS-only auth")
	}
}
