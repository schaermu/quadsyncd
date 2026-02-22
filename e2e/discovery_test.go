//go:build e2e_discovery

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schaermu/quadsyncd/e2e/harness"
)

const (
	repoPath    = "/home/quadsync/repo"
	configPath  = "/home/quadsync/.config/quadsyncd/config.yaml"
	quadletDir  = "/home/quadsync/.config/containers/systemd"
	stateDir    = "/home/quadsync/.local/state/quadsyncd"
	statePath   = "/home/quadsync/.local/state/quadsyncd/state.json"
	quadletFile = "/home/quadsync/.config/containers/systemd/hello.container"
)

func TestDiscovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	suite := harness.NewSuite("discovery", t)

	// Build image
	if err := suite.BuildImage(ctx); err != nil {
		t.Fatalf("build image: %v", err)
	}

	// Start container
	if err := suite.StartContainer(ctx); err != nil {
		t.Fatalf("start container: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()

		if err := suite.StopAndRemove(cleanupCtx); err != nil {
			t.Logf("cleanup: stop and remove container: %v", err)
		}
	}()

	// Run readiness probe
	if err := suite.Ready(ctx); err != nil {
		t.Fatalf("readiness probe failed: %v", err)
	}

	// Provision suite
	provisionSuite(t, suite, ctx)

	// Run scenarios
	t.Run("A_InitialSyncGeneratesUnit", func(t *testing.T) {
		testInitialSyncGeneratesUnit(t, suite, ctx)
	})

	t.Run("B_UpdateKeepsUnit", func(t *testing.T) {
		testUpdateKeepsUnit(t, suite, ctx)
	})

	t.Run("C_PruneRemovesUnit", func(t *testing.T) {
		testPruneRemovesUnit(t, suite, ctx)
	})
}

// provisionSuite sets up the test environment once per suite
func provisionSuite(t *testing.T, s *harness.Suite, ctx context.Context) {
	t.Helper()
	s.Logf("Provisioning suite")

	// Create config directory
	if err := s.MkdirUser(ctx, "/home/quadsync/.config/quadsyncd", 0755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	// Initialize git repo
	s.Logf("Initializing git repo at %s", repoPath)
	if _, err := s.MustExecUser(ctx, "git", "init", "-b", "main", repoPath); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := s.MustExecUser(ctx, "git", "-C", repoPath, "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if _, err := s.MustExecUser(ctx, "git", "-C", repoPath, "config", "user.name", "Test User"); err != nil {
		t.Fatalf("git config name: %v", err)
	}

	// Create initial quadlet file
	quadletContent := []byte(`[Unit]
Description=quadsyncd e2e hello

[Container]
ContainerName=quadsyncd-e2e-hello
Image=alpine:3.20
Exec=/bin/sleep 3600
`)
	if err := s.WriteFileUser(ctx, repoPath+"/hello.container", quadletContent, 0644); err != nil {
		t.Fatalf("write quadlet: %v", err)
	}

	// Commit
	if _, err := s.MustExecUser(ctx, "git", "-C", repoPath, "add", "hello.container"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := s.MustExecUser(ctx, "git", "-C", repoPath, "commit", "-m", "Initial commit"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	// Write quadsyncd config
	config := fmt.Sprintf(`repo:
  url: %s
  ref: main
  subdir: ""

paths:
  quadlet_dir: %s
  state_dir: %s

sync:
  prune: true
  restart: none
`, repoPath, quadletDir, stateDir)

	if err := s.WriteFileUser(ctx, configPath, []byte(config), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	s.Logf("Suite provisioned")
}

// testInitialSyncGeneratesUnit tests that initial sync generates a systemd unit
func testInitialSyncGeneratesUnit(t *testing.T, s *harness.Suite, ctx context.Context) {
	// Run quadsyncd sync
	res, err := s.MustExecUser(ctx, "quadsyncd", "sync", "--config", configPath)
	if err != nil {
		t.Fatalf("quadsyncd sync failed: %v", err)
	}
	t.Logf("quadsyncd stdout:\n%s", res.Stdout)
	if res.Stderr != "" {
		t.Logf("quadsyncd stderr:\n%s", res.Stderr)
	}

	// Assert quadlet file exists
	checkRes, _ := s.ExecUser(ctx, "test", "-f", quadletFile)
	if checkRes.ExitCode != 0 {
		t.Error("quadlet file does not exist")
	}

	// Assert state file exists
	checkRes, _ = s.ExecUser(ctx, "test", "-f", statePath)
	if checkRes.ExitCode != 0 {
		t.Error("state file does not exist")
	}

	// Read state and verify
	stateRes, err := s.ExecUser(ctx, "cat", statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !strings.Contains(stateRes.Stdout, "hello.container") {
		t.Error("state does not contain hello.container")
	}

	// Assert systemctl --user cat hello.service succeeds
	catRes, _ := s.ExecUser(ctx, "systemctl", "--user", "cat", "hello.service")
	if catRes.ExitCode != 0 {
		t.Errorf("systemctl --user cat hello.service failed\nstdout: %s\nstderr: %s",
			catRes.Stdout, catRes.Stderr)
	} else {
		t.Logf("hello.service generated:\n%s", catRes.Stdout)
	}
}

// testUpdateKeepsUnit tests that updating a quadlet keeps the unit
func testUpdateKeepsUnit(t *testing.T, s *harness.Suite, ctx context.Context) {
	// Modify quadlet in repo
	updatedQuadlet := []byte(`[Unit]
Description=quadsyncd e2e hello UPDATED

[Container]
ContainerName=quadsyncd-e2e-hello
Image=alpine:3.20
Exec=/bin/sleep 7200
`)
	if err := s.WriteFileUser(ctx, repoPath+"/hello.container", updatedQuadlet, 0644); err != nil {
		t.Fatalf("write updated quadlet: %v", err)
	}

	// Commit
	if _, err := s.MustExecUser(ctx, "git", "-C", repoPath, "add", "hello.container"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := s.MustExecUser(ctx, "git", "-C", repoPath, "commit", "-m", "Update hello.container"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	// Run quadsyncd sync
	res, err := s.MustExecUser(ctx, "quadsyncd", "sync", "--config", configPath)
	if err != nil {
		t.Fatalf("quadsyncd sync failed: %v", err)
	}
	t.Logf("quadsyncd stdout:\n%s", res.Stdout)

	// Assert destination file was updated
	contentRes, err := s.ExecUser(ctx, "cat", quadletFile)
	if err != nil {
		t.Fatalf("read quadlet: %v", err)
	}
	if !strings.Contains(contentRes.Stdout, "UPDATED") {
		t.Error("quadlet file not updated")
	}

	// Assert systemctl --user cat hello.service still succeeds
	catRes, _ := s.ExecUser(ctx, "systemctl", "--user", "cat", "hello.service")
	if catRes.ExitCode != 0 {
		t.Errorf("systemctl --user cat hello.service failed after update\nstdout: %s\nstderr: %s",
			catRes.Stdout, catRes.Stderr)
	}
}

// testPruneRemovesUnit tests that pruning removes the unit
func testPruneRemovesUnit(t *testing.T, s *harness.Suite, ctx context.Context) {
	// Delete quadlet from repo
	if _, err := s.MustExecUser(ctx, "git", "-C", repoPath, "rm", "hello.container"); err != nil {
		t.Fatalf("git rm: %v", err)
	}
	if _, err := s.MustExecUser(ctx, "git", "-C", repoPath, "commit", "-m", "Remove hello.container"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	// Run quadsyncd sync
	res, err := s.MustExecUser(ctx, "quadsyncd", "sync", "--config", configPath)
	if err != nil {
		t.Fatalf("quadsyncd sync failed: %v", err)
	}
	t.Logf("quadsyncd stdout:\n%s", res.Stdout)

	// Assert destination quadlet file was removed
	checkRes, _ := s.ExecUser(ctx, "test", "-f", quadletFile)
	if checkRes.ExitCode == 0 {
		t.Error("quadlet file still exists after prune")
	}

	// Assert systemctl --user cat hello.service fails
	catRes, _ := s.ExecUser(ctx, "systemctl", "--user", "cat", "hello.service")
	if catRes.ExitCode == 0 {
		t.Error("systemctl --user cat hello.service succeeded after prune (expected failure)")
	} else {
		t.Logf("hello.service correctly not found after prune")
	}
}
