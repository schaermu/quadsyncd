package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/schaermu/quadsyncd/internal/config"
)

// mockGitClient is a mock implementation of git.Client
type mockGitClient struct {
	checkoutCalled bool
	shouldFail     bool
}

func (m *mockGitClient) EnsureCheckout(ctx context.Context, url, ref, dest string) (string, error) {
	m.checkoutCalled = true
	if m.shouldFail {
		return "", http.ErrServerClosed
	}
	return "abc123", nil
}

// mockSystemd is a mock implementation of systemduser.Systemd
type mockSystemd struct {
	reloadCalled  bool
	restartCalled bool
	shouldFail    bool
}

func (m *mockSystemd) IsAvailable(ctx context.Context) (bool, error) {
	return !m.shouldFail, nil
}

func (m *mockSystemd) DaemonReload(ctx context.Context) error {
	m.reloadCalled = true
	if m.shouldFail {
		return http.ErrServerClosed
	}
	return nil
}

func (m *mockSystemd) TryRestartUnits(ctx context.Context, units []string) error {
	m.restartCalled = true
	if m.shouldFail {
		return http.ErrServerClosed
	}
	return nil
}

func setupTestConfig(t *testing.T) (*config.Config, string) {
	t.Helper()

	// Create temp directory for test
	tmpDir := t.TempDir()

	// Create secret file
	secretPath := filepath.Join(tmpDir, "webhook_secret")
	secret := "test-secret-key"
	if err := os.WriteFile(secretPath, []byte(secret), 0600); err != nil {
		t.Fatalf("failed to write secret file: %v", err)
	}

	cfg := &config.Config{
		Repo: config.RepoConfig{
			URL:    "https://github.com/test/repo.git",
			Ref:    "refs/heads/main",
			Subdir: "",
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
			ListenAddr:              "127.0.0.1:8787",
			GitHubWebhookSecretFile: secretPath,
			AllowedEventTypes:       []string{"push"},
			AllowedRefs:             []string{"refs/heads/main"},
		},
	}

	return cfg, secret
}

func computeSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestNewServer(t *testing.T) {
	cfg, _ := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGit, mockSys, logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	if server == nil {
		t.Fatal("expected server to be non-nil")
	}

	if string(server.secret) != "test-secret-key" {
		t.Errorf("expected secret to be 'test-secret-key', got %q", string(server.secret))
	}
}

func TestNewServer_MissingSecretFile(t *testing.T) {
	cfg, _ := setupTestConfig(t)
	cfg.Serve.GitHubWebhookSecretFile = "/nonexistent/secret"

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	_, err := NewServer(cfg, mockGit, mockSys, logger)
	if err == nil {
		t.Fatal("expected error for missing secret file, got nil")
	}
}

func TestStart_PerformsInitialSync(t *testing.T) {
	cfg, _ := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	if err := os.MkdirAll(cfg.Paths.QuadletDir, 0755); err != nil {
		t.Fatalf("failed to create quadlet dir: %v", err)
	}
	if err := os.MkdirAll(cfg.Paths.StateDir, 0755); err != nil {
		t.Fatalf("failed to create state dir: %v", err)
	}

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGit, mockSys, logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	// Cancel the context immediately so Start returns after the initial sync
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = server.Start(ctx)

	if !mockGit.checkoutCalled {
		t.Error("expected initial sync to call git checkout, but it was not called")
	}
}

func TestVerifySignature(t *testing.T) {
	cfg, secret := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGit, mockSys, logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	tests := []struct {
		name      string
		body      []byte
		signature string
		want      bool
	}{
		{
			name:      "valid signature",
			body:      []byte(`{"ref":"refs/heads/main"}`),
			signature: computeSignature([]byte(`{"ref":"refs/heads/main"}`), secret),
			want:      true,
		},
		{
			name:      "invalid signature",
			body:      []byte(`{"ref":"refs/heads/main"}`),
			signature: "sha256=invalid",
			want:      false,
		},
		{
			name:      "missing sha256 prefix",
			body:      []byte(`{"ref":"refs/heads/main"}`),
			signature: "notsha256",
			want:      false,
		},
		{
			name:      "empty signature",
			body:      []byte(`{"ref":"refs/heads/main"}`),
			signature: "",
			want:      false,
		},
		{
			name:      "wrong body",
			body:      []byte(`{"ref":"refs/heads/other"}`),
			signature: computeSignature([]byte(`{"ref":"refs/heads/main"}`), secret),
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := server.verifySignature(tt.body, tt.signature)
			if got != tt.want {
				t.Errorf("verifySignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsEventTypeAllowed(t *testing.T) {
	cfg, _ := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	tests := []struct {
		name              string
		allowedEventTypes []string
		eventType         string
		want              bool
	}{
		{
			name:              "allowed event",
			allowedEventTypes: []string{"push", "pull_request"},
			eventType:         "push",
			want:              true,
		},
		{
			name:              "disallowed event",
			allowedEventTypes: []string{"push"},
			eventType:         "pull_request",
			want:              false,
		},
		{
			name:              "no filter (allow all)",
			allowedEventTypes: []string{},
			eventType:         "anything",
			want:              true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg.Serve.AllowedEventTypes = tt.allowedEventTypes

			mockGit := &mockGitClient{}
			mockSys := &mockSystemd{}

			server, err := NewServer(cfg, mockGit, mockSys, logger)
			if err != nil {
				t.Fatalf("NewServer() failed: %v", err)
			}

			got := server.isEventTypeAllowed(tt.eventType)
			if got != tt.want {
				t.Errorf("isEventTypeAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsRefAllowed(t *testing.T) {
	cfg, _ := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	tests := []struct {
		name        string
		allowedRefs []string
		ref         string
		want        bool
	}{
		{
			name:        "allowed ref",
			allowedRefs: []string{"refs/heads/main", "refs/heads/develop"},
			ref:         "refs/heads/main",
			want:        true,
		},
		{
			name:        "disallowed ref",
			allowedRefs: []string{"refs/heads/main"},
			ref:         "refs/heads/feature",
			want:        false,
		},
		{
			name:        "no filter (allow all)",
			allowedRefs: []string{},
			ref:         "refs/heads/anything",
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg.Serve.AllowedRefs = tt.allowedRefs

			mockGit := &mockGitClient{}
			mockSys := &mockSystemd{}

			server, err := NewServer(cfg, mockGit, mockSys, logger)
			if err != nil {
				t.Fatalf("NewServer() failed: %v", err)
			}

			got := server.isRefAllowed(tt.ref)
			if got != tt.want {
				t.Errorf("isRefAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleWebhook_ValidRequest(t *testing.T) {
	cfg, secret := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Create temp directories for quadlets
	if err := os.MkdirAll(cfg.Paths.QuadletDir, 0755); err != nil {
		t.Fatalf("failed to create quadlet dir: %v", err)
	}
	if err := os.MkdirAll(cfg.Paths.StateDir, 0755); err != nil {
		t.Fatalf("failed to create state dir: %v", err)
	}

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGit, mockSys, logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	body := []byte(`{
		"ref": "refs/heads/main",
		"after": "abc123",
		"repository": {
			"full_name": "test/repo"
		}
	}`)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", computeSignature(body, secret))

	rec := httptest.NewRecorder()
	server.handleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	// Wait a bit for debounced sync to potentially trigger
	time.Sleep(50 * time.Millisecond)
}

func TestHandleWebhook_InvalidMethod(t *testing.T) {
	cfg, _ := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGit, mockSys, logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	server.handleWebhook(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}

func TestHandleWebhook_InvalidContentType(t *testing.T) {
	cfg, _ := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGit, mockSys, logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "text/plain")

	rec := httptest.NewRecorder()
	server.handleWebhook(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestHandleWebhook_InvalidSignature(t *testing.T) {
	cfg, _ := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGit, mockSys, logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	body := []byte(`{"ref":"refs/heads/main"}`)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")

	rec := httptest.NewRecorder()
	server.handleWebhook(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", rec.Code)
	}
}

func TestHandleWebhook_DisallowedEventType(t *testing.T) {
	cfg, secret := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGit, mockSys, logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	body := []byte(`{"ref":"refs/heads/main"}`)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", computeSignature(body, secret))

	rec := httptest.NewRecorder()
	server.handleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	// Should return success but not trigger sync
	if !bytes.Contains(rec.Body.Bytes(), []byte("Event type not configured")) {
		t.Errorf("expected 'Event type not configured' message, got: %s", rec.Body.String())
	}
}

func TestHandleWebhook_DisallowedRef(t *testing.T) {
	cfg, secret := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGit, mockSys, logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	body := []byte(`{
		"ref": "refs/heads/feature",
		"after": "abc123",
		"repository": {
			"full_name": "test/repo"
		}
	}`)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", computeSignature(body, secret))

	rec := httptest.NewRecorder()
	server.handleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	// Should return success but not trigger sync
	if !bytes.Contains(rec.Body.Bytes(), []byte("Ref not configured")) {
		t.Errorf("expected 'Ref not configured' message, got: %s", rec.Body.String())
	}
}

func TestDebouncer(t *testing.T) {
	var callCount int
	var mu sync.Mutex
	d := &debouncer{delay: 50 * time.Millisecond}

	// Trigger multiple times rapidly
	for i := 0; i < 5; i++ {
		d.trigger(func() {
			mu.Lock()
			callCount++
			mu.Unlock()
		})
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for debounce to complete
	time.Sleep(100 * time.Millisecond)

	// Should only be called once despite 5 triggers
	mu.Lock()
	count := callCount
	mu.Unlock()

	if count != 1 {
		t.Errorf("expected callback to be called once, got %d", count)
	}
}

// TestPerformSync_SingleFlight verifies that concurrent performSync calls use
// single-flight semantics: at most one sync runs at a time and at most one
// additional run is queued; excess concurrent requests are dropped.
func TestPerformSync_SingleFlight(t *testing.T) {
	cfg, _ := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	if err := os.MkdirAll(cfg.Paths.QuadletDir, 0755); err != nil {
		t.Fatalf("failed to create quadlet dir: %v", err)
	}
	if err := os.MkdirAll(cfg.Paths.StateDir, 0755); err != nil {
		t.Fatalf("failed to create state dir: %v", err)
	}

	// Use a slow git client to keep the first sync in-flight long enough for
	// concurrent callers to arrive.
	syncStarted := make(chan struct{})
	syncProceed := make(chan struct{})

	slowGit := &slowMockGitClient{
		started: syncStarted,
		proceed: syncProceed,
	}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, slowGit, mockSys, logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	ctx := context.Background()

	// Start first sync in background; it will block until syncProceed is closed.
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.performSync(ctx)
	}()

	// Wait until the first sync has started (git checkout entered).
	<-syncStarted

	// Fire three more concurrent performSync calls while the first is running.
	// Only one of these should queue a pending re-run; the other two are dropped.
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			server.performSync(ctx)
		}()
	}
	wg.Wait()

	// Exactly one pending sync should have been recorded.
	server.syncMu.Lock()
	pending := server.syncPending
	server.syncMu.Unlock()

	if !pending {
		t.Error("expected syncPending to be true after concurrent performSync calls")
	}

	// Allow the first sync to complete; the server should then service the
	// single pending re-run automatically.
	close(syncProceed)
	<-done // performSync only returns once all pending syncs have completed

	server.syncMu.Lock()
	stillRunning := server.syncRunning
	stillPending := server.syncPending
	server.syncMu.Unlock()

	if stillRunning {
		t.Error("expected syncRunning to be false after all syncs completed")
	}
	if stillPending {
		t.Error("expected syncPending to be false after pending re-run was serviced")
	}
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
