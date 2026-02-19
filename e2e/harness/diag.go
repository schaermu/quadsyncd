//go:build e2e_discovery

package harness

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Diagnostics represents collected diagnostic information
type Diagnostics struct {
	CollectedAt time.Time
	Items       []DiagItem
}

// DiagItem represents a single diagnostic command output
type DiagItem struct {
	Name     string
	Cmd      []string
	ExitCode int
	Output   string
}

// CollectDiagnostics gathers diagnostic information from the container
func (s *Suite) CollectDiagnostics(ctx context.Context) (*Diagnostics, error) {
	diag := &Diagnostics{
		CollectedAt: time.Now(),
		Items:       []DiagItem{},
	}

	// Root diagnostics
	rootCommands := []struct {
		name string
		cmd  []string
	}{
		{"systemctl-status", []string{"systemctl", "status", "--no-pager"}},
		{"systemctl-user-service", []string{"systemctl", "status", fmt.Sprintf("user@%d.service", s.UID), "--no-pager"}},
		{"loginctl-user-status", []string{"loginctl", "user-status", s.User, "--no-pager"}},
		{"journalctl-boot", []string{"journalctl", "-b", "--no-pager", "-n", "300"}},
		{"docker-logs", nil}, // special case, handled separately
	}

	for _, item := range rootCommands {
		if item.name == "docker-logs" {
			// Special case: get docker logs from outside the container
			output, exitCode := s.getDockerLogs(ctx)
			diag.Items = append(diag.Items, DiagItem{
				Name:     item.name,
				Cmd:      []string{"docker", "logs", s.ContainerID},
				ExitCode: exitCode,
				Output:   output,
			})
			continue
		}

		res, _ := s.ExecRoot(ctx, item.cmd...)
		diag.Items = append(diag.Items, DiagItem{
			Name:     item.name,
			Cmd:      item.cmd,
			ExitCode: res.ExitCode,
			Output:   res.Stdout + res.Stderr,
		})
	}

	// User diagnostics
	userCommands := []struct {
		name string
		cmd  []string
	}{
		{"systemctl-user-status", []string{"systemctl", "--user", "status", "--no-pager"}},
		{"journalctl-user", []string{"journalctl", "--user", "--no-pager", "-n", "300"}},
		{"systemctl-user-cat-hello", []string{"systemctl", "--user", "cat", "hello.service", "--no-pager"}},
		{"ls-quadlet-dir", []string{"ls", "-la", s.Home + "/.config/containers/systemd"}},
		{"ls-state-dir", []string{"ls", "-la", s.Home + "/.local/state/quadsyncd"}},
		{"cat-state-json", []string{"cat", s.Home + "/.local/state/quadsyncd/state.json"}},
	}

	for _, item := range userCommands {
		res, _ := s.ExecUser(ctx, item.cmd...)
		diag.Items = append(diag.Items, DiagItem{
			Name:     item.name,
			Cmd:      item.cmd,
			ExitCode: res.ExitCode,
			Output:   res.Stdout + res.Stderr,
		})
	}

	return diag, nil
}

// getDockerLogs gets docker logs from outside the container
func (s *Suite) getDockerLogs(ctx context.Context) (string, int) {
	if s.ContainerID == "" {
		return "", 0
	}

	cmd := exec.CommandContext(ctx, "docker", "logs", s.ContainerID)
	output, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	return string(output), exitCode
}

// DumpDiagnostics collects and logs diagnostic information
func (s *Suite) DumpDiagnostics(ctx context.Context) {
	s.Logf("=== Collecting diagnostics ===")

	diag, err := s.CollectDiagnostics(ctx)
	if err != nil {
		s.Logf("Failed to collect diagnostics: %v", err)
		return
	}

	for _, item := range diag.Items {
		s.Logf("--- %s (exit %d) ---", item.Name, item.ExitCode)
		s.Logf("Command: %s", strings.Join(item.Cmd, " "))
		if item.Output != "" {
			s.Logf("%s", item.Output)
		} else {
			s.Logf("(no output)")
		}
	}

	s.Logf("=== End diagnostics ===")
}

// RunScenario runs a test scenario and collects diagnostics on failure
func (s *Suite) RunScenario(ctx context.Context, name string, fn func(context.Context) error) error {
	s.Logf("Running scenario: %s", name)
	err := fn(ctx)
	if err != nil {
		s.Logf("Scenario %s failed: %v", name, err)
		s.DumpDiagnostics(ctx)
	} else {
		s.Logf("Scenario %s passed", name)
	}
	return err
}
