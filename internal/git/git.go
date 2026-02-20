package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Client provides git operations for repository management
type Client interface {
	// EnsureCheckout clones or updates a repository to the specified ref
	EnsureCheckout(ctx context.Context, url, ref, destDir string) (string, error)
}

// ShellClient implements Client by shelling out to the git command
type ShellClient struct {
	sshKeyFile     string
	httpsTokenFile string
}

// NewShellClient creates a new git client that uses the git command
func NewShellClient(sshKeyFile, httpsTokenFile string) *ShellClient {
	return &ShellClient{
		sshKeyFile:     sshKeyFile,
		httpsTokenFile: httpsTokenFile,
	}
}

// EnsureCheckout clones or fetches and checks out the specified ref
func (c *ShellClient) EnsureCheckout(ctx context.Context, url, ref, destDir string) (string, error) {
	// Check if repo already exists
	gitDir := filepath.Join(destDir, ".git")
	exists := false
	if _, err := os.Stat(gitDir); err == nil {
		exists = true
	}

	var cmd *exec.Cmd
	if !exists {
		// Clone the repository
		if err := os.MkdirAll(filepath.Dir(destDir), 0755); err != nil {
			return "", fmt.Errorf("failed to create parent directory: %w", err)
		}

		cmd = exec.CommandContext(ctx, "git", "clone", "--no-checkout", url, destDir)
		if err := c.configureAuth(cmd, url); err != nil {
			return "", err
		}

		if err := c.runCommand(cmd); err != nil {
			return "", fmt.Errorf("git clone failed: %w", err)
		}
	} else {
		// Fetch updates
		cmd = exec.CommandContext(ctx, "git", "-C", destDir, "fetch", "origin")
		if err := c.configureAuth(cmd, url); err != nil {
			return "", err
		}

		if err := c.runCommand(cmd); err != nil {
			return "", fmt.Errorf("git fetch failed: %w", err)
		}
	}

	// Checkout the specified ref
	// Strategy:
	// 1. Try direct checkout (works for local branches, tags, commit hashes)
	// 2. If that fails, try as a remote branch (origin/ref)
	// This handles tags and commit hashes correctly, and prefers local refs when they exist
	cmd = exec.CommandContext(ctx, "git", "-C", destDir, "checkout", "-f", ref)
	if err := c.runCommand(cmd); err != nil {
		// If direct checkout failed, try as a remote branch
		remoteRef := "origin/" + ref
		cmd = exec.CommandContext(ctx, "git", "-C", destDir, "checkout", "-f", remoteRef)
		if err := c.runCommand(cmd); err != nil {
			return "", fmt.Errorf("git checkout failed for ref %q (tried both direct and remote): %w", ref, err)
		}
	}

	// For existing repos, the local branch may be stale after fetch.
	// Reset to the remote tracking branch to pick up new commits.
	// This is a no-op for fresh clones and silently ignored for tags/hashes.
	if exists {
		resetCmd := exec.CommandContext(ctx, "git", "-C", destDir, "reset", "--hard", "origin/"+ref)
		_ = c.runCommand(resetCmd)
	}

	// Get the commit hash
	cmd = exec.CommandContext(ctx, "git", "-C", destDir, "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse failed: %w", err)
	}

	commit := strings.TrimSpace(string(output))
	return commit, nil
}

// configureAuth sets up authentication for git operations
func (c *ShellClient) configureAuth(cmd *exec.Cmd, url string) error {
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}

	// SSH authentication
	if c.sshKeyFile != "" && (strings.HasPrefix(url, "git@") || strings.HasPrefix(url, "ssh://")) {
		// Use GIT_SSH_COMMAND to specify the SSH key.
		// The path is shell-quoted to prevent injection via crafted filenames.
		sshCmd := fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=accept-new -F /dev/null", shellQuote(c.sshKeyFile))
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+sshCmd)
		return nil
	}

	// HTTPS authentication with token
	if c.httpsTokenFile != "" && strings.HasPrefix(url, "https://") {
		token, err := os.ReadFile(c.httpsTokenFile)
		if err != nil {
			return fmt.Errorf("failed to read HTTPS token file: %w", err)
		}

		tokenStr := strings.TrimSpace(string(token))

		// Pass the token via environment variable and configure a git
		// credential helper that reads it. This avoids embedding the
		// token directly in a shell expression.
		cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
		cmd.Env = append(cmd.Env, "QUADSYNCD_GIT_TOKEN="+tokenStr)
		cmd.Args = insertGitFlags(cmd.Args,
			"-c", `credential.helper=!f() { echo "username=x-access-token"; echo "password=$QUADSYNCD_GIT_TOKEN"; }; f`,
		)

		return nil
	}

	return nil
}

// insertGitFlags inserts flags immediately after the "git" command name,
// before the subcommand (e.g. "clone", "fetch").
func insertGitFlags(args []string, flags ...string) []string {
	if len(args) == 0 {
		return flags
	}
	result := make([]string, 0, len(args)+len(flags))
	result = append(result, args[0])
	result = append(result, flags...)
	result = append(result, args[1:]...)
	return result
}

// shellQuote wraps s in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runCommand executes a command and returns an error with stderr on failure
func (c *ShellClient) runCommand(cmd *exec.Cmd) error {
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(output))
	}
	return nil
}
