//go:build e2e_discovery

package harness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	defaultImageTag      = "quadsyncd-e2e-sut:latest"
	defaultDockerfileDir = "./docker/sut-systemd"
	defaultTimeout       = 10 * time.Minute
	defaultUser          = "quadsync"
	defaultUID           = 1000
	defaultHome          = "/home/quadsync"
)

// Suite orchestrates E2E tests in a systemd container
type Suite struct {
	// immutable config
	Name          string
	ImageTag      string
	DockerfileDir string
	Timeout       time.Duration
	KeepContainer bool

	// runtime state
	ContainerID string
	UID         int
	User        string
	Home        string

	// computed env for user exec
	UserEnv map[string]string

	// optional logger hook
	Logf func(format string, args ...any)

	// test reference
	t *testing.T
}

// SuiteOption configures a Suite
type SuiteOption func(*Suite)

// WithImageTag sets a custom image tag
func WithImageTag(tag string) SuiteOption {
	return func(s *Suite) { s.ImageTag = tag }
}

// WithDockerfileDir sets a custom dockerfile directory
func WithDockerfileDir(dir string) SuiteOption {
	return func(s *Suite) { s.DockerfileDir = dir }
}

// WithTimeout sets a custom suite timeout
func WithTimeout(d time.Duration) SuiteOption {
	return func(s *Suite) { s.Timeout = d }
}

// WithKeepContainer sets whether to keep the container on failure
func WithKeepContainer(v bool) SuiteOption {
	return func(s *Suite) { s.KeepContainer = v }
}

// WithUser sets custom user details
func WithUser(user string, uid int, home string) SuiteOption {
	return func(s *Suite) {
		s.User = user
		s.UID = uid
		s.Home = home
	}
}

// WithLogf sets a custom logger
func WithLogf(logf func(string, ...any)) SuiteOption {
	return func(s *Suite) { s.Logf = logf }
}

// NewSuite creates a new E2E test suite
func NewSuite(name string, t *testing.T, opts ...SuiteOption) *Suite {
	s := &Suite{
		Name:          name,
		ImageTag:      defaultImageTag,
		DockerfileDir: defaultDockerfileDir,
		Timeout:       defaultTimeout,
		KeepContainer: os.Getenv("E2E_KEEP_CONTAINER") == "1",
		UID:           defaultUID,
		User:          defaultUser,
		Home:          defaultHome,
		UserEnv:       make(map[string]string),
		t:             t,
		Logf:          t.Logf,
	}

	for _, opt := range opts {
		opt(s)
	}

	// Check for env overrides
	if tag := os.Getenv("E2E_SUT_TAG"); tag != "" {
		s.ImageTag = tag
	}
	if timeout := os.Getenv("E2E_TIMEOUT"); timeout != "" {
		if d, err := time.ParseDuration(timeout); err == nil {
			s.Timeout = d
		}
	}

	return s
}

// BuildImage builds the systemd SUT container image
func (s *Suite) BuildImage(ctx context.Context) error {
	s.Logf("Building image %s from %s", s.ImageTag, s.DockerfileDir)

	// Get absolute path to project root (one level up from e2e)
	projectRoot, err := filepath.Abs("..")
	if err != nil {
		return fmt.Errorf("get project root: %w", err)
	}

	cmd := exec.CommandContext(ctx,
		"docker", "build",
		"-t", s.ImageTag,
		"-f", filepath.Join(s.DockerfileDir, "Dockerfile"),
		projectRoot, // build context is project root
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		s.Logf("build stdout: %s", stdout.String())
		s.Logf("build stderr: %s", stderr.String())
		return fmt.Errorf("docker build: %w", err)
	}

	s.Logf("Image %s built successfully", s.ImageTag)
	return nil
}

// StartContainer starts the systemd container with required flags
func (s *Suite) StartContainer(ctx context.Context) error {
	s.Logf("Starting container")

	cmd := exec.CommandContext(ctx,
		"docker", "run",
		"-d",
		"--rm",
		"--privileged",
		"--cgroupns=host",
		"-v", "/sys/fs/cgroup:/sys/fs/cgroup:rw",
		"--tmpfs", "/run",
		"--tmpfs", "/tmp",
		"-e", "container=docker",
		s.ImageTag,
	)

	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("docker run: %w", err)
	}

	s.ContainerID = strings.TrimSpace(string(out))
	s.Logf("Container started: %s", s.ContainerID)
	return nil
}

// StopAndRemove stops and removes the container
func (s *Suite) StopAndRemove(ctx context.Context) error {
	if s.ContainerID == "" {
		return nil
	}

	if s.KeepContainer && s.t.Failed() {
		s.Logf("Test failed and E2E_KEEP_CONTAINER=1, keeping container %s", s.ContainerID)
		s.Logf("To inspect: docker exec -it %s /bin/bash", s.ContainerID)
		s.Logf("To cleanup: docker stop %s", s.ContainerID)
		return nil
	}

	s.Logf("Stopping container %s", s.ContainerID)
	cmd := exec.CommandContext(ctx, "docker", "stop", s.ContainerID)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker stop: %w", err)
	}

	return nil
}

// Ready performs the critical readiness probe
func (s *Suite) Ready(ctx context.Context) error {
	s.Logf("Running readiness probe")

	// 1) Wait for PID1 systemd
	s.Logf("Waiting for systemd to be ready")
	if err := s.waitForSystemd(ctx); err != nil {
		return fmt.Errorf("systemd not ready: %w", err)
	}

	// 2) Ensure user manager exists
	s.Logf("Enabling linger for %s", s.User)
	if _, err := s.MustExecRoot(ctx, "loginctl", "enable-linger", s.User); err != nil {
		return fmt.Errorf("enable-linger: %w", err)
	}

	s.Logf("Starting user@%d.service", s.UID)
	if _, err := s.MustExecRoot(ctx, "systemctl", "start", fmt.Sprintf("user@%d.service", s.UID)); err != nil {
		return fmt.Errorf("start user manager: %w", err)
	}

	// 3) Ensure runtime dir exists
	runtimeDir := fmt.Sprintf("/run/user/%d", s.UID)
	s.Logf("Ensuring runtime dir %s", runtimeDir)
	if _, err := s.MustExecRoot(ctx, "mkdir", "-p", runtimeDir); err != nil {
		return fmt.Errorf("mkdir runtime dir: %w", err)
	}
	if _, err := s.MustExecRoot(ctx, "chown", fmt.Sprintf("%d:%d", s.UID, s.UID), runtimeDir); err != nil {
		return fmt.Errorf("chown runtime dir: %w", err)
	}
	if _, err := s.MustExecRoot(ctx, "chmod", "0700", runtimeDir); err != nil {
		return fmt.Errorf("chmod runtime dir: %w", err)
	}

	// 4) Establish user exec env
	s.UserEnv["HOME"] = s.Home
	s.UserEnv["XDG_RUNTIME_DIR"] = runtimeDir

	// Check if dbus session bus exists
	busPath := runtimeDir + "/bus"
	res, _ := s.ExecRoot(ctx, "test", "-S", busPath)
	if res.ExitCode == 0 {
		s.UserEnv["DBUS_SESSION_BUS_ADDRESS"] = "unix:path=" + busPath
		s.Logf("Found session bus at %s", busPath)
	}

	// 5) Definitive probe: systemctl --user status
	s.Logf("Running definitive probe: systemctl --user status")
	res, err := s.ExecUser(ctx, "systemctl", "--user", "status")
	if err != nil {
		s.DumpDiagnostics(ctx)
		return fmt.Errorf("user systemctl probe failed: %w", err)
	}
	if res.ExitCode != 0 {
		s.DumpDiagnostics(ctx)
		return fmt.Errorf("systemctl --user status failed with exit %d\nstdout: %s\nstderr: %s",
			res.ExitCode, res.Stdout, res.Stderr)
	}

	s.Logf("Readiness probe passed")
	return nil
}

// waitForSystemd waits for systemd to be ready
func (s *Suite) waitForSystemd(ctx context.Context) error {
	timeout := time.After(2 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting for systemd")
		case <-ticker.C:
			res, _ := s.ExecRoot(ctx, "systemctl", "is-system-running", "--wait")
			// Accept "running" or "degraded"
			status := strings.TrimSpace(res.Stdout)
			if status == "running" || status == "degraded" {
				s.Logf("systemd is %s", status)
				return nil
			}
			s.Logf("systemd status: %s (waiting...)", status)
		}
	}
}

// ExecResult represents the result of a command execution
type ExecResult struct {
	Cmd      []string
	ExitCode int
	Stdout   string
	Stderr   string
}

// ExecRoot executes a command as root in the container
func (s *Suite) ExecRoot(ctx context.Context, cmd ...string) (ExecResult, error) {
	if s.ContainerID == "" {
		return ExecResult{}, fmt.Errorf("container not started")
	}

	args := append([]string{"exec", s.ContainerID}, cmd...)
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
			return ExecResult{}, fmt.Errorf("exec failed: %w", err)
		}
	}

	return ExecResult{
		Cmd:      cmd,
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, nil
}

// ExecUser executes a command as the suite user with user environment
func (s *Suite) ExecUser(ctx context.Context, cmd ...string) (ExecResult, error) {
	return s.ExecUserEnv(ctx, nil, cmd...)
}

// ExecUserEnv executes a command as the suite user with custom environment
func (s *Suite) ExecUserEnv(ctx context.Context, env map[string]string, cmd ...string) (ExecResult, error) {
	if s.ContainerID == "" {
		return ExecResult{}, fmt.Errorf("container not started")
	}

	// Merge suite user env with custom env
	finalEnv := make(map[string]string)
	for k, v := range s.UserEnv {
		finalEnv[k] = v
	}
	for k, v := range env {
		finalEnv[k] = v
	}

	// Build docker exec command
	args := []string{"exec"}
	for k, v := range finalEnv {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	args = append(args, "-u", fmt.Sprintf("%d:%d", s.UID, s.UID))
	args = append(args, s.ContainerID)
	args = append(args, cmd...)

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
			return ExecResult{}, fmt.Errorf("exec failed: %w", err)
		}
	}

	return ExecResult{
		Cmd:      cmd,
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, nil
}

// MustExecRoot executes a command as root and fails on non-zero exit
func (s *Suite) MustExecRoot(ctx context.Context, cmd ...string) (ExecResult, error) {
	res, err := s.ExecRoot(ctx, cmd...)
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("command failed with exit %d: %v\nstdout: %s\nstderr: %s",
			res.ExitCode, cmd, res.Stdout, res.Stderr)
	}
	return res, nil
}

// MustExecUser executes a command as user and fails on non-zero exit
func (s *Suite) MustExecUser(ctx context.Context, cmd ...string) (ExecResult, error) {
	res, err := s.ExecUser(ctx, cmd...)
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("command failed with exit %d: %v\nstdout: %s\nstderr: %s",
			res.ExitCode, cmd, res.Stdout, res.Stderr)
	}
	return res, nil
}

// WriteFileRoot writes a file as root
func (s *Suite) WriteFileRoot(ctx context.Context, path string, content []byte, mode os.FileMode) error {
	// Create parent directory
	dir := filepath.Dir(path)
	if dir != "." && dir != "/" {
		if _, err := s.MustExecRoot(ctx, "mkdir", "-p", dir); err != nil {
			return fmt.Errorf("mkdir parent: %w", err)
		}
	}

	// Write file
	cmd := exec.CommandContext(ctx,
		"docker", "exec", "-i", s.ContainerID,
		"sh", "-c", fmt.Sprintf("cat > %s", path),
	)
	cmd.Stdin = bytes.NewReader(content)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	// Set permissions
	if _, err := s.MustExecRoot(ctx, "chmod", fmt.Sprintf("%o", mode), path); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	return nil
}

// WriteFileUser writes a file as the suite user
func (s *Suite) WriteFileUser(ctx context.Context, path string, content []byte, mode os.FileMode) error {
	// Create parent directory as user
	dir := filepath.Dir(path)
	if dir != "." && dir != "/" {
		if _, err := s.MustExecUser(ctx, "mkdir", "-p", dir); err != nil {
			return fmt.Errorf("mkdir parent: %w", err)
		}
	}

	// Write file as user
	args := []string{"exec", "-i"}
	for k, v := range s.UserEnv {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	args = append(args, "-u", fmt.Sprintf("%d:%d", s.UID, s.UID))
	args = append(args, s.ContainerID)
	args = append(args, "sh", "-c", fmt.Sprintf("cat > %s", path))

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = bytes.NewReader(content)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	// Set permissions as user
	if _, err := s.MustExecUser(ctx, "chmod", fmt.Sprintf("%o", mode), path); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	return nil
}

// MkdirUser creates a directory as the suite user
func (s *Suite) MkdirUser(ctx context.Context, path string, mode os.FileMode) error {
	if _, err := s.MustExecUser(ctx, "mkdir", "-p", path); err != nil {
		return err
	}
	if _, err := s.MustExecUser(ctx, "chmod", fmt.Sprintf("%o", mode), path); err != nil {
		return err
	}
	return nil
}
