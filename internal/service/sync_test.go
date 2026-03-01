package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/git"
	"github.com/schaermu/quadsyncd/internal/runstore"
	quadsyncd "github.com/schaermu/quadsyncd/internal/sync"
	"github.com/schaermu/quadsyncd/internal/testutil"
)

// mockRunner implements sync.Runner for unit-testing executeSync in isolation.
// It returns the configured result and error, and logs a message containing
// secretToLog (if set) so redaction tests can verify the tee logger strips it.
type mockRunner struct {
	result      *quadsyncd.Result
	err         error
	secretToLog string
	logger      *slog.Logger
	called      bool
}

func (m *mockRunner) Run(_ context.Context) (*quadsyncd.Result, error) {
	m.called = true
	if m.secretToLog != "" && m.logger != nil {
		m.logger.Info("connecting with secret", "token", m.secretToLog)
	}
	return m.result, m.err
}

// newMockRunnerFactory returns a RunnerFactory that always returns the given
// mockRunner, capturing the logger passed at construction time.
func newMockRunnerFactory(mr *mockRunner) quadsyncd.RunnerFactory {
	return func(_ *config.Config, logger *slog.Logger, _ bool, _ *quadsyncd.PlanEngineOptions) quadsyncd.Runner {
		mr.logger = logger
		return mr
	}
}

// newMockSyncService builds a SyncService with a MockRunStore and the given
// RunnerFactory + secret. This allows tests to exercise executeSync paths
// without a real git client or filesystem.
func newMockSyncService(t *testing.T, store *testutil.MockRunStore, factory quadsyncd.RunnerFactory, secret string) *SyncService {
	t.Helper()
	cfg := &config.Config{
		Repository: &config.RepoSpec{
			URL: "https://github.com/test/repo.git",
			Ref: "refs/heads/main",
		},
		Paths: config.PathsConfig{
			QuadletDir: t.TempDir(),
			StateDir:   t.TempDir(),
		},
		Sync: config.SyncConfig{Restart: config.RestartChanged},
	}
	logger := testutil.TestLogger()
	return NewSyncService(cfg, factory, store, logger, []byte(secret))
}

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

// TestExecuteSync_Success verifies the happy path: store.Create succeeds,
// sync returns a result with revisions and conflicts, and store.Update
// is called with correct final state.
func TestExecuteSync_Success(t *testing.T) {
	store := testutil.NewMockRunStore()
	mr := &mockRunner{
		result: &quadsyncd.Result{
			Revisions: map[string]string{
				"https://github.com/test/repo.git": "abc123",
			},
			Conflicts: []quadsyncd.Conflict{
				{
					MergeKey:   "app.container",
					WinnerRepo: "https://github.com/test/repo.git",
					WinnerRef:  "refs/heads/main",
					WinnerSHA:  "abc123",
					Losers: []quadsyncd.ConflictLoser{
						{Repo: "https://github.com/other/repo.git", Ref: "refs/heads/main", SHA: "def456"},
					},
				},
			},
		},
	}
	svc := newMockSyncService(t, store, newMockRunnerFactory(mr), "my-secret")

	svc.TriggerSync(context.Background(), runstore.TriggerWebhook)

	if !mr.called {
		t.Fatal("expected runner to be called")
	}

	// Exactly one run should exist.
	runs, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	run := runs[0]
	if run.Status != runstore.RunStatusSuccess {
		t.Errorf("expected status %q, got %q", runstore.RunStatusSuccess, run.Status)
	}
	if run.EndedAt == nil {
		t.Error("expected EndedAt to be set")
	}
	if run.Revisions["https://github.com/test/repo.git"] != "abc123" {
		t.Errorf("expected revision abc123, got %q", run.Revisions["https://github.com/test/repo.git"])
	}
	if len(run.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(run.Conflicts))
	}
	if run.Conflicts[0].MergeKey != "app.container" {
		t.Errorf("expected conflict merge key %q, got %q", "app.container", run.Conflicts[0].MergeKey)
	}
	if run.Conflicts[0].Winner.SourceRepo != "https://github.com/test/repo.git" {
		t.Errorf("unexpected winner repo: %q", run.Conflicts[0].Winner.SourceRepo)
	}
	if len(run.Conflicts[0].Losers) != 1 || run.Conflicts[0].Losers[0].SourceRepo != "https://github.com/other/repo.git" {
		t.Errorf("unexpected losers: %+v", run.Conflicts[0].Losers)
	}
}

// TestExecuteSync_SyncError verifies that when the runner returns an error,
// the run record is updated with status "error" and the error message.
func TestExecuteSync_SyncError(t *testing.T) {
	store := testutil.NewMockRunStore()
	mr := &mockRunner{
		err: errors.New("git fetch timeout"),
	}
	svc := newMockSyncService(t, store, newMockRunnerFactory(mr), "secret")

	svc.TriggerSync(context.Background(), runstore.TriggerCLI)

	runs, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	run := runs[0]
	if run.Status != runstore.RunStatusError {
		t.Errorf("expected status %q, got %q", runstore.RunStatusError, run.Status)
	}
	if run.Error != "git fetch timeout" {
		t.Errorf("expected error %q, got %q", "git fetch timeout", run.Error)
	}
}

// TestExecuteSync_StoreCreateFails_FallbackRuns verifies the best-effort
// fallback: when store.Create fails, sync still executes but without
// instrumentation (no run record is stored).
func TestExecuteSync_StoreCreateFails_FallbackRuns(t *testing.T) {
	store := testutil.NewMockRunStore()
	store.CreateFunc = func(_ context.Context, _ *runstore.RunMeta) error {
		return fmt.Errorf("disk full")
	}
	mr := &mockRunner{
		result: &quadsyncd.Result{
			Revisions: map[string]string{"repo": "sha1"},
		},
	}
	svc := newMockSyncService(t, store, newMockRunnerFactory(mr), "secret")

	svc.TriggerSync(context.Background(), runstore.TriggerWebhook)

	if !mr.called {
		t.Fatal("expected runner to be called despite store.Create failure")
	}

	// No run should be stored because Create failed.
	runs, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs (Create failed), got %d", len(runs))
	}
}

// TestExecuteSync_StoreCreateFails_SyncAlsoFails verifies that when both
// store.Create and the sync runner fail, the service does not panic.
func TestExecuteSync_StoreCreateFails_SyncAlsoFails(t *testing.T) {
	store := testutil.NewMockRunStore()
	store.CreateFunc = func(_ context.Context, _ *runstore.RunMeta) error {
		return fmt.Errorf("disk full")
	}
	mr := &mockRunner{
		err: errors.New("git clone failed"),
	}
	svc := newMockSyncService(t, store, newMockRunnerFactory(mr), "secret")

	// Should not panic.
	svc.TriggerSync(context.Background(), runstore.TriggerWebhook)

	if !mr.called {
		t.Fatal("expected runner to be called despite store.Create failure")
	}
}

// TestExecuteSync_StoreUpdateFails_NoError verifies that a store.Update
// failure is logged but does not cause TriggerSync to panic or propagate
// the error (fire-and-forget semantics for persistence).
func TestExecuteSync_StoreUpdateFails_NoError(t *testing.T) {
	store := testutil.NewMockRunStore()
	store.UpdateFunc = func(_ context.Context, _ *runstore.RunMeta) error {
		return fmt.Errorf("i/o error")
	}
	mr := &mockRunner{
		result: &quadsyncd.Result{
			Revisions: map[string]string{"repo": "sha1"},
		},
	}
	svc := newMockSyncService(t, store, newMockRunnerFactory(mr), "secret")

	// Should not panic despite Update failing.
	svc.TriggerSync(context.Background(), runstore.TriggerWebhook)

	if !mr.called {
		t.Fatal("expected runner to be called")
	}
}

// TestExecuteSync_NilResult verifies that a nil result from the runner
// (possible when an error occurs before any work is done) does not cause
// a nil-pointer dereference when mapping revisions/conflicts.
func TestExecuteSync_NilResult(t *testing.T) {
	store := testutil.NewMockRunStore()
	mr := &mockRunner{
		result: nil,
		err:    errors.New("early failure"),
	}
	svc := newMockSyncService(t, store, newMockRunnerFactory(mr), "secret")

	// Should not panic on nil result.
	svc.TriggerSync(context.Background(), runstore.TriggerWebhook)

	runs, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Status != runstore.RunStatusError {
		t.Errorf("expected status %q, got %q", runstore.RunStatusError, runs[0].Status)
	}
	// Revisions should remain the empty map initialised in executeSync.
	if len(runs[0].Revisions) != 0 {
		t.Errorf("expected empty revisions for nil result, got %v", runs[0].Revisions)
	}
}

// TestExecuteSync_SecretRedaction verifies that the tee logger redacts
// known secrets from NDJSON run logs written to the store.
func TestExecuteSync_SecretRedaction(t *testing.T) {
	store := testutil.NewMockRunStore()
	secret := "super-secret-webhook-token"
	mr := &mockRunner{
		result:      &quadsyncd.Result{Revisions: map[string]string{}},
		secretToLog: secret,
	}
	svc := newMockSyncService(t, store, newMockRunnerFactory(mr), secret)

	svc.TriggerSync(context.Background(), runstore.TriggerWebhook)

	// Find the run ID.
	runs, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	// Read the NDJSON log lines stored for this run.
	logRecords, err := store.ReadLog(context.Background(), runs[0].ID)
	if err != nil {
		t.Fatalf("store.ReadLog: %v", err)
	}

	if len(logRecords) == 0 {
		t.Fatal("expected at least one log record")
	}

	// Verify that no log line contains the raw secret.
	for i, rec := range logRecords {
		for key, val := range rec {
			s, ok := val.(string)
			if !ok {
				continue
			}
			if strings.Contains(s, secret) {
				t.Errorf("log record %d, key %q contains raw secret: %q", i, key, s)
			}
		}
	}

	// At least one record should contain [REDACTED] (from the "token" field).
	found := false
	for _, rec := range logRecords {
		for _, val := range rec {
			s, ok := val.(string)
			if !ok {
				continue
			}
			if strings.Contains(s, "[REDACTED]") {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Error("expected at least one log record to contain [REDACTED]")
	}
}

// TestExecuteSync_TriggerSourcePreserved verifies that the trigger source
// passed to TriggerSync is correctly persisted in the run record.
func TestExecuteSync_TriggerSourcePreserved(t *testing.T) {
	for _, trigger := range []runstore.TriggerSource{
		runstore.TriggerWebhook,
		runstore.TriggerCLI,
		runstore.TriggerTimer,
		runstore.TriggerStartup,
		runstore.TriggerUI,
	} {
		t.Run(string(trigger), func(t *testing.T) {
			store := testutil.NewMockRunStore()
			mr := &mockRunner{result: &quadsyncd.Result{Revisions: map[string]string{}}}
			svc := newMockSyncService(t, store, newMockRunnerFactory(mr), "secret")

			svc.TriggerSync(context.Background(), trigger)

			runs, err := store.List(context.Background())
			if err != nil {
				t.Fatalf("store.List: %v", err)
			}
			if len(runs) != 1 {
				t.Fatalf("expected 1 run, got %d", len(runs))
			}
			if runs[0].Trigger != trigger {
				t.Errorf("expected trigger %q, got %q", trigger, runs[0].Trigger)
			}
		})
	}
}
