//go:build integration

package tier1

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	imageTag       = "quadsyncd-tier1-sut:latest"
	dockerfileDir  = "./docker"
	shimLogPath    = "/tmp/systemctl.log"
	defaultTimeout = 5 * time.Minute
)

// Harness provides container orchestration for Tier 1 integration tests
type Harness struct {
	t           *testing.T
	containerID string
	imageTag    string
	keepOnFail  bool
}

// NewHarness creates a new test harness
func NewHarness(t *testing.T) *Harness {
	t.Helper()
	return &Harness{
		t:          t,
		imageTag:   imageTag,
		keepOnFail: os.Getenv("INTEGRATION_KEEP_CONTAINER") == "1",
	}
}

// BuildImage builds the test container image
func (h *Harness) BuildImage(ctx context.Context) error {
	h.t.Helper()
	h.t.Logf("Building image %s from %s", h.imageTag, dockerfileDir)

	// Get absolute path to project root by finding go.mod
	projectRoot, err := findProjectRoot()
	if err != nil {
		return fmt.Errorf("get project root: %w", err)
	}

	cmd := exec.CommandContext(ctx,
		"docker", "build",
		"-t", h.imageTag,
		"-f", filepath.Join(dockerfileDir, "Dockerfile"),
		projectRoot, // build context is project root
	)
	cmd.Stdout = &testWriter{t: h.t, prefix: "[build] "}
	cmd.Stderr = &testWriter{t: h.t, prefix: "[build] "}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}

	h.t.Logf("Image %s built successfully", h.imageTag)
	return nil
}

// StartContainer starts the test container
func (h *Harness) StartContainer(ctx context.Context) error {
	h.t.Helper()
	h.t.Log("Starting container")

	cmd := exec.CommandContext(ctx,
		"docker", "run",
		"-d",
		"--rm",
		h.imageTag,
	)

	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("docker run: %w", err)
	}

	h.containerID = strings.TrimSpace(string(out))
	h.t.Logf("Container started: %s", h.containerID)
	return nil
}

// Cleanup stops and removes the container
func (h *Harness) Cleanup(ctx context.Context) {
	h.t.Helper()
	if h.containerID == "" {
		return
	}

	if h.keepOnFail && h.t.Failed() {
		h.t.Logf("Test failed and INTEGRATION_KEEP_CONTAINER=1, keeping container %s", h.containerID)
		h.t.Logf("To inspect: docker exec -it %s /bin/sh", h.containerID)
		h.t.Logf("To cleanup: docker stop %s", h.containerID)
		return
	}

	h.t.Logf("Stopping container %s", h.containerID)
	cmd := exec.CommandContext(ctx, "docker", "stop", h.containerID)
	if err := cmd.Run(); err != nil {
		h.t.Logf("Warning: failed to stop container: %v", err)
	}
}

// Exec executes a command in the container
func (h *Harness) Exec(ctx context.Context, cmd ...string) (string, string, int, error) {
	h.t.Helper()
	if h.containerID == "" {
		return "", "", 0, fmt.Errorf("container not started")
	}

	args := append([]string{"exec", h.containerID}, cmd...)
	execCmd := exec.CommandContext(ctx, "docker", args...)

	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	err := execCmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", "", 0, fmt.Errorf("exec failed: %w", err)
		}
	}

	return stdout.String(), stderr.String(), exitCode, nil
}

// MustExec executes a command and fails the test if it returns non-zero
func (h *Harness) MustExec(ctx context.Context, cmd ...string) (string, string) {
	h.t.Helper()
	stdout, stderr, exitCode, err := h.Exec(ctx, cmd...)
	if err != nil {
		h.t.Fatalf("exec failed: %v", err)
	}
	if exitCode != 0 {
		h.t.Fatalf("command failed with exit code %d\nstdout: %s\nstderr: %s\ncmd: %v",
			exitCode, stdout, stderr, cmd)
	}
	return stdout, stderr
}

// WriteFile writes a file to the container
func (h *Harness) WriteFile(ctx context.Context, path, content string) error {
	h.t.Helper()
	if h.containerID == "" {
		return fmt.Errorf("container not started")
	}

	// Create parent directory
	dir := filepath.Dir(path)
	if dir != "." && dir != "/" {
		_, _, _, err := h.Exec(ctx, "mkdir", "-p", dir)
		if err != nil {
			return fmt.Errorf("mkdir parent: %w", err)
		}
	}

	// Write file using sh -c with cat
	cmd := exec.CommandContext(ctx,
		"docker", "exec", "-i", h.containerID,
		"sh", "-c", fmt.Sprintf("cat > %s", path),
	)
	cmd.Stdin = strings.NewReader(content)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	return nil
}

// ReadFile reads a file from the container
func (h *Harness) ReadFile(ctx context.Context, path string) (string, error) {
	h.t.Helper()
	stdout, _, exitCode, err := h.Exec(ctx, "cat", path)
	if err != nil {
		return "", err
	}
	if exitCode != 0 {
		return "", fmt.Errorf("cat failed with exit code %d", exitCode)
	}
	return stdout, nil
}

// ReadShimLog reads and parses the systemctl shim log
func (h *Harness) ReadShimLog(ctx context.Context) ([]ShimLogEntry, error) {
	h.t.Helper()
	content, err := h.ReadFile(ctx, shimLogPath)
	if err != nil {
		return nil, err
	}

	var entries []ShimLogEntry
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Parse: "2024-01-01T12:00:00+00:00 --user daemon-reload"
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}

		timestamp := parts[0]
		args := strings.Fields(parts[1])

		entries = append(entries, ShimLogEntry{
			Timestamp: timestamp,
			Args:      args,
		})
	}

	return entries, scanner.Err()
}

// ClearShimLog clears the systemctl shim log
func (h *Harness) ClearShimLog(ctx context.Context) error {
	h.t.Helper()
	_, _, _, err := h.Exec(ctx, "sh", "-c", fmt.Sprintf("> %s", shimLogPath))
	return err
}

// FileExists checks if a file exists in the container
func (h *Harness) FileExists(ctx context.Context, path string) bool {
	h.t.Helper()
	_, _, exitCode, _ := h.Exec(ctx, "test", "-f", path)
	return exitCode == 0
}

// ShimLogEntry represents a parsed systemctl shim log entry
type ShimLogEntry struct {
	Timestamp string
	Args      []string
}

// String returns a human-readable representation
func (e ShimLogEntry) String() string {
	return fmt.Sprintf("%s: systemctl %s", e.Timestamp, strings.Join(e.Args, " "))
}

// HasArgs checks if the entry contains the given arguments
func (e ShimLogEntry) HasArgs(args ...string) bool {
	if len(e.Args) < len(args) {
		return false
	}
	for i, arg := range args {
		if e.Args[i] != arg {
			return false
		}
	}
	return true
}

// ContainsArg checks if the entry contains a specific argument anywhere
func (e ShimLogEntry) ContainsArg(arg string) bool {
	for _, a := range e.Args {
		if a == arg {
			return true
		}
	}
	return false
}

// testWriter wraps test logging for command output
type testWriter struct {
	t      *testing.T
	prefix string
}

func (w *testWriter) Write(p []byte) (n int, err error) {
	lines := strings.Split(string(p), "\n")
	for _, line := range lines {
		if line != "" {
			w.t.Log(w.prefix + line)
		}
	}
	return len(p), nil
}

var _ io.Writer = (*testWriter)(nil)

// findProjectRoot walks up the directory tree from the current file to find go.mod
func findProjectRoot() (string, error) {
	// Get the directory of this source file
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("failed to get caller information")
	}

	dir := filepath.Dir(filename)

	// Walk up the directory tree looking for go.mod
	for {
		goModPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the root without finding go.mod
			return "", fmt.Errorf("go.mod not found in any parent directory")
		}
		dir = parent
	}
}
