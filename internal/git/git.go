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
	cmd = exec.CommandContext(ctx, "git", "-C", destDir, "checkout", "-f", ref)
	if err := c.runCommand(cmd); err != nil {
		return "", fmt.Errorf("git checkout failed: %w", err)
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
		// Use GIT_SSH_COMMAND to specify the SSH key
		sshCmd := fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=accept-new -F /dev/null", c.sshKeyFile)
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+sshCmd)
		return nil
	}

	// HTTPS authentication with token
	if c.httpsTokenFile != "" && strings.HasPrefix(url, "https://") {
		token, err := os.ReadFile(c.httpsTokenFile)
		if err != nil {
			return fmt.Errorf("failed to read HTTPS token file: %w", err)
		}

		// Configure git credential helper to use the token
		tokenStr := strings.TrimSpace(string(token))

		// For GitHub, we can use the token as username with x-oauth-basic
		// Set GIT_ASKPASS to provide credentials non-interactively
		helper := fmt.Sprintf("!f() { echo '%s'; }; f", tokenStr)
		cmd.Env = append(cmd.Env, "GIT_ASKPASS="+helper)

		return nil
	}

	return nil
}

// runCommand executes a command and returns an error with stderr on failure
func (c *ShellClient) runCommand(cmd *exec.Cmd) error {
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(output))
	}
	return nil
}
