package service

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/git"
	"github.com/schaermu/quadsyncd/internal/runstore"
	quadsyncd "github.com/schaermu/quadsyncd/internal/sync"
	"github.com/schaermu/quadsyncd/internal/testutil"
)

// slowMockGitClient blocks EnsureCheckout until proceed is closed, allowing
// tests to control sync concurrency.
type slowMockGitClient struct {
	started chan struct{}
	proceed chan struct{}
	once    sync.Once
}

func (m *slowMockGitClient) EnsureCheckout(_ context.Context, _, _, _ string) (string, error) {
	m.once.Do(func() { close(m.started) })
	<-m.proceed
	return "abc123", nil
}

// newTestSyncService builds a SyncService wired to the given git client for testing.
func newTestSyncService(t *testing.T, gitClient git.Client) (*SyncService, *config.Config) {
	t.Helper()

	tmpDir := t.TempDir()
	secretPath := filepath.Join(tmpDir, "secret")
	if err := os.WriteFile(secretPath, []byte("test-secret"), 0600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	cfg := &config.Config{
		Repository: &config.RepoSpec{
			URL: "https://github.com/test/repo.git",
			Ref: "refs/heads/main",
		},
		Paths: config.PathsConfig{
			QuadletDir: filepath.Join(tmpDir, "quadlets"),
			StateDir:   filepath.Join(tmpDir, "state"),
		},
		Sync: config.SyncConfig{
			Prune:   true,
			Restart: config.RestartChanged,
		},
		Serve: config.ServeConfig{
			Enabled:                 true,
			ListenAddr:              "127.0.0.1:0",
			GitHubWebhookSecretFile: secretPath,
		},
	}

	for _, d := range []string{cfg.Paths.QuadletDir, cfg.Paths.StateDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("MkdirAll %s: %v", d, err)
		}
	}

	logger := testutil.TestLogger()
	mockSys := &testutil.MockSystemd{Available: true}
	store := runstore.NewStore(cfg.Paths.StateDir, logger)
	factory := quadsyncd.NewRunnerFactory(
		func(_ config.AuthConfig) git.Client { return gitClient },
		mockSys,
	)

	svc := NewSyncService(cfg, factory, store, logger, []byte("test-secret"))
	return svc, cfg
}

// TestSyncService_SingleFlight verifies that concurrent TriggerSync calls use
// single-flight semantics: at most one sync runs at a time, at most one
// additional run is queued, and excess concurrent calls are dropped.
func TestSyncService_SingleFlight(t *testing.T) {
	syncStarted := make(chan struct{})
	syncProceed := make(chan struct{})
	slowGit := &slowMockGitClient{started: syncStarted, proceed: syncProceed}

	svc, _ := newTestSyncService(t, slowGit)
	ctx := context.Background()

	// Start the first sync in a background goroutine; it will block until
	// syncProceed is closed.
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.TriggerSync(ctx, runstore.TriggerWebhook)
	}()

	// Wait until the first sync has entered the git checkout.
	<-syncStarted

	// Fire three more concurrent TriggerSync calls while the first is in-flight.
	// Only one should queue a pending re-run; the other two should be dropped.
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.TriggerSync(ctx, runstore.TriggerWebhook)
		}()
	}
	wg.Wait()

	// Exactly one pending sync should have been queued.
	svc.mu.Lock()
	pending := svc.pending
	svc.mu.Unlock()

	if !pending {
		t.Error("expected pending to be true after concurrent TriggerSync calls")
	}

	// Allow the first sync to complete; the service should service the pending
	// re-run automatically and then return.
	close(syncProceed)
	<-done

	svc.mu.Lock()
	stillRunning := svc.running
	stillPending := svc.pending
	svc.mu.Unlock()

	if stillRunning {
		t.Error("expected running to be false after all syncs completed")
	}
	if stillPending {
		t.Error("expected pending to be false after pending re-run was serviced")
	}
}
