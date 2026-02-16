//go:build integration

package tier1

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	// Test paths (inside container)
	testRepoPath    = "/test/repo"
	testConfigPath  = "/test/config/config.yaml"
	testQuadletDir  = "/test/quadlet"
	testStateDir    = "/test/state"
	testStatePath   = "/test/state/state.json"
	testQuadletFile = "/test/quadlet/hello.container"
	testSSHKeyPath  = "/dev/null" // dummy, unused for local repo
)

func TestTier1Sync(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	h := NewHarness(t)

	// Build image
	if err := h.BuildImage(ctx); err != nil {
		t.Fatalf("build image: %v", err)
	}

	// Start container
	if err := h.StartContainer(ctx); err != nil {
		t.Fatalf("start container: %v", err)
	}
	defer h.Cleanup(ctx)

	// Wait for container to be ready
	time.Sleep(1 * time.Second)

	// Setup git repo
	setupGitRepo(t, h, ctx)

	// Run all scenarios as subtests
	t.Run("A_InitialSyncRestartNone", func(t *testing.T) {
		testInitialSyncRestartNone(t, h, ctx)
	})

	t.Run("B_InitialSyncRestartChanged", func(t *testing.T) {
		// Clear state and quadlets from previous test
		resetState(t, h, ctx)
		testInitialSyncRestartChanged(t, h, ctx)
	})

	t.Run("C_UpdateSyncRestartChanged", func(t *testing.T) {
		testUpdateSyncRestartChanged(t, h, ctx)
	})

	t.Run("D_NoOpSync", func(t *testing.T) {
		testNoOpSync(t, h, ctx)
	})

	t.Run("E_PruneRemovesFile", func(t *testing.T) {
		testPruneRemovesFile(t, h, ctx)
	})

	t.Run("F_DryRunMode", func(t *testing.T) {
		// Reset for clean dry-run test
		resetState(t, h, ctx)
		testDryRunMode(t, h, ctx)
	})
}

// setupGitRepo initializes a git repo in the container with a sample quadlet
func setupGitRepo(t *testing.T, h *Harness, ctx context.Context) {
	t.Helper()

	// Initialize git repo with main branch
	h.MustExec(ctx, "git", "init", "-b", "main", testRepoPath)
	h.MustExec(ctx, "git", "-C", testRepoPath, "config", "user.email", "test@example.com")
	h.MustExec(ctx, "git", "-C", testRepoPath, "config", "user.name", "Test User")

	// Create initial quadlet file
	quadletContent := `[Unit]
Description=quadsyncd tier1 hello

[Container]
ContainerName=quadsyncd-tier1-hello
Image=alpine:3.20
Exec=/bin/sleep 3600
`
	if err := h.WriteFile(ctx, testRepoPath+"/hello.container", quadletContent); err != nil {
		t.Fatalf("write quadlet: %v", err)
	}

	// Commit
	h.MustExec(ctx, "git", "-C", testRepoPath, "add", "hello.container")
	h.MustExec(ctx, "git", "-C", testRepoPath, "commit", "-m", "Initial commit")
}

// resetState clears quadlet files, state, and shim log
func resetState(t *testing.T, h *Harness, ctx context.Context) {
	t.Helper()
	h.MustExec(ctx, "rm", "-rf", testQuadletDir, testStateDir)
	h.MustExec(ctx, "mkdir", "-p", testQuadletDir, testStateDir)
	if err := h.ClearShimLog(ctx); err != nil {
		t.Fatalf("clear shim log: %v", err)
	}
}

// writeConfig writes a quadsyncd config file
func writeConfig(t *testing.T, h *Harness, ctx context.Context, restart string, prune bool) {
	t.Helper()

	config := fmt.Sprintf(`repo:
  url: %s
  ref: main
  subdir: ""

paths:
  quadlet_dir: %s
  state_dir: %s

sync:
  prune: %t
  restart: %s

auth:
  ssh_key_file: %s
`, testRepoPath, testQuadletDir, testStateDir, prune, restart, testSSHKeyPath)

	if err := h.WriteFile(ctx, testConfigPath, config); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// testInitialSyncRestartNone tests initial sync with restart policy "none"
func testInitialSyncRestartNone(t *testing.T, h *Harness, ctx context.Context) {
	writeConfig(t, h, ctx, "none", false)
	if err := h.ClearShimLog(ctx); err != nil {
		t.Fatalf("clear shim log: %v", err)
	}

	// Run sync
	stdout, stderr := h.MustExec(ctx, "quadsyncd", "sync", "--config", testConfigPath)
	t.Logf("stdout: %s", stdout)
	t.Logf("stderr: %s", stderr)

	// Assert quadlet file exists
	if !h.FileExists(ctx, testQuadletFile) {
		t.Error("quadlet file does not exist")
	}

	// Assert state file exists
	if !h.FileExists(ctx, testStatePath) {
		t.Error("state file does not exist")
	}

	// Read state and verify
	stateContent, err := h.ReadFile(ctx, testStatePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !strings.Contains(stateContent, "hello.container") {
		t.Error("state does not contain hello.container")
	}

	// Read shim log
	entries, err := h.ReadShimLog(ctx)
	if err != nil {
		t.Fatalf("read shim log: %v", err)
	}

	// Assert daemon-reload was called
	foundReload := false
	foundRestart := false
	for _, entry := range entries {
		t.Logf("shim: %s", entry)
		if entry.ContainsArg("daemon-reload") {
			foundReload = true
		}
		if entry.ContainsArg("try-restart") {
			foundRestart = true
		}
	}

	if !foundReload {
		t.Error("daemon-reload not called")
	}
	if foundRestart {
		t.Error("try-restart called with restart:none")
	}
}

// testInitialSyncRestartChanged tests initial sync with restart policy "changed"
func testInitialSyncRestartChanged(t *testing.T, h *Harness, ctx context.Context) {
	writeConfig(t, h, ctx, "changed", false)
	if err := h.ClearShimLog(ctx); err != nil {
		t.Fatalf("clear shim log: %v", err)
	}

	// Run sync
	stdout, stderr := h.MustExec(ctx, "quadsyncd", "sync", "--config", testConfigPath)
	t.Logf("stdout: %s", stdout)
	t.Logf("stderr: %s", stderr)

	// Assert quadlet file exists
	if !h.FileExists(ctx, testQuadletFile) {
		t.Error("quadlet file does not exist")
	}

	// Assert state file exists
	if !h.FileExists(ctx, testStatePath) {
		t.Error("state file does not exist")
	}

	// Read shim log
	entries, err := h.ReadShimLog(ctx)
	if err != nil {
		t.Fatalf("read shim log: %v", err)
	}

	// Assert daemon-reload and try-restart were called
	foundReload := false
	foundRestart := false
	for _, entry := range entries {
		t.Logf("shim: %s", entry)
		if entry.ContainsArg("daemon-reload") {
			foundReload = true
		}
		if entry.ContainsArg("try-restart") && entry.ContainsArg("hello.service") {
			foundRestart = true
		}
	}

	if !foundReload {
		t.Error("daemon-reload not called")
	}
	if !foundRestart {
		t.Error("try-restart hello.service not called")
	}
}

// testUpdateSyncRestartChanged tests updating a quadlet with restart policy "changed"
func testUpdateSyncRestartChanged(t *testing.T, h *Harness, ctx context.Context) {
	writeConfig(t, h, ctx, "changed", false)

	// Modify quadlet in repo
	updatedQuadlet := `[Unit]
Description=quadsyncd tier1 hello UPDATED

[Container]
ContainerName=quadsyncd-tier1-hello
Image=alpine:3.20
Exec=/bin/sleep 7200
`
	if err := h.WriteFile(ctx, testRepoPath+"/hello.container", updatedQuadlet); err != nil {
		t.Fatalf("write updated quadlet: %v", err)
	}

	// Commit
	h.MustExec(ctx, "git", "-C", testRepoPath, "add", "hello.container")
	h.MustExec(ctx, "git", "-C", testRepoPath, "commit", "-m", "Update hello.container")

	// Clear shim log
	if err := h.ClearShimLog(ctx); err != nil {
		t.Fatalf("clear shim log: %v", err)
	}

	// Run sync
	stdout, stderr := h.MustExec(ctx, "quadsyncd", "sync", "--config", testConfigPath)
	t.Logf("stdout: %s", stdout)
	t.Logf("stderr: %s", stderr)

	// Assert file was updated
	content, err := h.ReadFile(ctx, testQuadletFile)
	if err != nil {
		t.Fatalf("read quadlet: %v", err)
	}
	if !strings.Contains(content, "UPDATED") {
		t.Error("quadlet file not updated")
	}

	// Read shim log
	entries, err := h.ReadShimLog(ctx)
	if err != nil {
		t.Fatalf("read shim log: %v", err)
	}

	// Assert daemon-reload and try-restart were called
	foundReload := false
	foundRestart := false
	for _, entry := range entries {
		t.Logf("shim: %s", entry)
		if entry.ContainsArg("daemon-reload") {
			foundReload = true
		}
		if entry.ContainsArg("try-restart") && entry.ContainsArg("hello.service") {
			foundRestart = true
		}
	}

	if !foundReload {
		t.Error("daemon-reload not called")
	}
	if !foundRestart {
		t.Error("try-restart hello.service not called")
	}
}

// testNoOpSync tests sync with no changes
func testNoOpSync(t *testing.T, h *Harness, ctx context.Context) {
	writeConfig(t, h, ctx, "changed", false)

	// Clear shim log
	if err := h.ClearShimLog(ctx); err != nil {
		t.Fatalf("clear shim log: %v", err)
	}

	// Get state before sync
	stateBefore, err := h.ReadFile(ctx, testStatePath)
	if err != nil {
		t.Fatalf("read state before: %v", err)
	}

	// Run sync
	stdout, stderr := h.MustExec(ctx, "quadsyncd", "sync", "--config", testConfigPath)
	t.Logf("stdout: %s", stdout)
	t.Logf("stderr: %s", stderr)

	// Assert state unchanged
	stateAfter, err := h.ReadFile(ctx, testStatePath)
	if err != nil {
		t.Fatalf("read state after: %v", err)
	}
	if stateBefore != stateAfter {
		t.Error("state changed on no-op sync")
	}

	// Read shim log
	entries, err := h.ReadShimLog(ctx)
	if err != nil {
		t.Fatalf("read shim log: %v", err)
	}

	// Assert daemon-reload was called but not try-restart
	foundReload := false
	foundRestart := false
	for _, entry := range entries {
		t.Logf("shim: %s", entry)
		if entry.ContainsArg("daemon-reload") {
			foundReload = true
		}
		if entry.ContainsArg("try-restart") {
			foundRestart = true
		}
	}

	if !foundReload {
		t.Error("daemon-reload not called")
	}
	if foundRestart {
		t.Error("try-restart called on no-op sync")
	}
}

// testPruneRemovesFile tests pruning deleted files
func testPruneRemovesFile(t *testing.T, h *Harness, ctx context.Context) {
	writeConfig(t, h, ctx, "changed", true) // prune: true

	// Delete quadlet from repo
	h.MustExec(ctx, "git", "-C", testRepoPath, "rm", "hello.container")
	h.MustExec(ctx, "git", "-C", testRepoPath, "commit", "-m", "Remove hello.container")

	// Clear shim log
	if err := h.ClearShimLog(ctx); err != nil {
		t.Fatalf("clear shim log: %v", err)
	}

	// Run sync
	stdout, stderr := h.MustExec(ctx, "quadsyncd", "sync", "--config", testConfigPath)
	t.Logf("stdout: %s", stdout)
	t.Logf("stderr: %s", stderr)

	// Assert file was removed
	if h.FileExists(ctx, testQuadletFile) {
		t.Error("quadlet file still exists after prune")
	}

	// Assert state no longer references the file
	stateContent, err := h.ReadFile(ctx, testStatePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(stateContent, "hello.container") {
		t.Error("state still contains hello.container")
	}

	// Read shim log
	entries, err := h.ReadShimLog(ctx)
	if err != nil {
		t.Fatalf("read shim log: %v", err)
	}

	// Assert daemon-reload was called
	foundReload := false
	for _, entry := range entries {
		t.Logf("shim: %s", entry)
		if entry.ContainsArg("daemon-reload") {
			foundReload = true
		}
	}

	if !foundReload {
		t.Error("daemon-reload not called")
	}
}

// testDryRunMode tests dry-run mode
func testDryRunMode(t *testing.T, h *Harness, ctx context.Context) {
	writeConfig(t, h, ctx, "changed", false)

	// Ensure quadlet doesn't exist
	h.MustExec(ctx, "rm", "-f", testQuadletFile)

	// Clear shim log
	if err := h.ClearShimLog(ctx); err != nil {
		t.Fatalf("clear shim log: %v", err)
	}

	// Run sync with --dry-run
	stdout, stderr, exitCode, err := h.Exec(ctx, "quadsyncd", "sync", "--config", testConfigPath, "--dry-run")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("dry-run failed: exit %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	t.Logf("stdout: %s", stdout)
	t.Logf("stderr: %s", stderr)

	// Assert no files written
	if h.FileExists(ctx, testQuadletFile) {
		t.Error("quadlet file exists after dry-run")
	}

	// Assert state unchanged (should not exist)
	if h.FileExists(ctx, testStatePath) {
		t.Error("state file exists after dry-run")
	}

	// Assert shim log is empty (no systemctl calls)
	entries, err := h.ReadShimLog(ctx)
	if err != nil && err.Error() != "exit status 1" {
		t.Fatalf("read shim log: %v", err)
	}
	if len(entries) > 0 {
		t.Errorf("systemctl called during dry-run: %v", entries)
	}

	// Assert stdout contains plan details
	if !strings.Contains(stdout, "DRY RUN") && !strings.Contains(stdout, "Would") && !strings.Contains(stderr, "dry-run") {
		t.Error("stdout does not indicate dry-run mode")
	}
}
