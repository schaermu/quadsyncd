package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/git"
	"github.com/schaermu/quadsyncd/internal/runstore"
)

// mockGitClient is a mock implementation of git.Client
type mockGitClient struct {
	checkoutCalled bool
	shouldFail     bool
	commit         string
	repoSetup      func(destDir string)
}

func (m *mockGitClient) EnsureCheckout(ctx context.Context, url, ref, dest string) (string, error) {
	m.checkoutCalled = true
	if m.shouldFail {
		return "", http.ErrServerClosed
	}
	if m.repoSetup != nil {
		m.repoSetup(dest)
	}
	commit := m.commit
	if commit == "" {
		commit = "abc123"
	}
	return commit, nil
}

// mockGitFactory returns a GitClientFactory that always returns the given mock.
func mockGitFactory(mock *mockGitClient) GitClientFactory {
	return func(_ config.AuthConfig) git.Client { return mock }
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

func (m *mockSystemd) ValidateQuadlets(_ context.Context, _ string) error {
	if m.shouldFail {
		return http.ErrServerClosed
	}
	return nil
}

func (m *mockSystemd) GetUnitStatus(_ context.Context, _ string) (string, error) {
	if m.shouldFail {
		return "failed", http.ErrServerClosed
	}
	return "inactive", nil
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
		Repository: &config.RepoSpec{
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
	store := runstore.NewStore(cfg.Paths.StateDir, logger)

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, store, logger)
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
	store := runstore.NewStore(cfg.Paths.StateDir, logger)
	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	_, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, store, logger)
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

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
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

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
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

			server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
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

			server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
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

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
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

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
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

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
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

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader([]byte("{}")))
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

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
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

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
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

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
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

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
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

	server, err := NewServer(cfg, func(_ config.AuthConfig) git.Client { return slowGit }, mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	ctx := context.Background()

	// Start first sync in background; it will block until syncProceed is closed.
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.performSync(ctx, runstore.TriggerWebhook)
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
			server.performSync(ctx, runstore.TriggerWebhook)
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

func TestStartWithListener(t *testing.T) {
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

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	// Create a listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer func() {
		_ = listener.Close()
	}()

	// Cancel the context immediately so StartWithListener returns after the initial sync
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = server.StartWithListener(ctx, listener)

	if !mockGit.checkoutCalled {
		t.Error("expected initial sync to call git checkout, but it was not called")
	}
}

func TestSliceContains(t *testing.T) {
	tests := []struct {
		name  string
		slice []string
		s     string
		want  bool
	}{
		{name: "found", slice: []string{"a", "b", "c"}, s: "b", want: true},
		{name: "not found", slice: []string{"a", "b", "c"}, s: "d", want: false},
		{name: "empty slice", slice: []string{}, s: "a", want: false},
		{name: "nil slice", slice: nil, s: "a", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sliceContains(tt.slice, tt.s)
			if got != tt.want {
				t.Errorf("sliceContains(%v, %q) = %v, want %v", tt.slice, tt.s, got, tt.want)
			}
		})
	}
}

func TestRepoFullNameFromURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "https with .git", url: "https://github.com/org/repo.git", want: "org/repo"},
		{name: "https without .git", url: "https://github.com/org/repo", want: "org/repo"},
		{name: "ssh shorthand with .git", url: "git@github.com:org/repo.git", want: "org/repo"},
		{name: "ssh shorthand without .git", url: "git@github.com:org/repo", want: "org/repo"},
		{name: "ssh scheme", url: "ssh://git@github.com/org/repo.git", want: "org/repo"},
		{name: "http", url: "http://github.com/org/repo.git", want: "org/repo"},
		{name: "empty", url: "", want: ""},
		{name: "no slash after host", url: "https://github.com", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := repoFullNameFromURL(tt.url)
			if got != tt.want {
				t.Errorf("repoFullNameFromURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestMatchesConfiguredRepo(t *testing.T) {
	cfg, _ := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	tests := []struct {
		name  string
		repos []config.RepoSpec
		event GitHubPushEvent
		want  bool
	}{
		{
			name: "single repo matching URL and ref",
			repos: []config.RepoSpec{
				{URL: "https://github.com/org/repo.git", Ref: "refs/heads/main"},
			},
			event: makeEvent("org/repo", "https://github.com/org/repo.git", "git@github.com:org/repo.git", "refs/heads/main"),
			want:  true,
		},
		{
			name: "single repo matching URL wrong ref",
			repos: []config.RepoSpec{
				{URL: "https://github.com/org/repo.git", Ref: "refs/heads/main"},
			},
			event: makeEvent("org/repo", "https://github.com/org/repo.git", "git@github.com:org/repo.git", "refs/heads/develop"),
			want:  false,
		},
		{
			name: "single repo wrong URL",
			repos: []config.RepoSpec{
				{URL: "https://github.com/org/other.git", Ref: "refs/heads/main"},
			},
			event: makeEvent("org/repo", "https://github.com/org/repo.git", "git@github.com:org/repo.git", "refs/heads/main"),
			want:  false,
		},
		{
			name: "multi repo first matches",
			repos: []config.RepoSpec{
				{URL: "https://github.com/org/repo1.git", Ref: "refs/heads/main"},
				{URL: "git@github.com:org/repo2.git", Ref: "refs/heads/stable"},
			},
			event: makeEvent("org/repo1", "https://github.com/org/repo1.git", "git@github.com:org/repo1.git", "refs/heads/main"),
			want:  true,
		},
		{
			name: "multi repo second matches via SSH",
			repos: []config.RepoSpec{
				{URL: "https://github.com/org/repo1.git", Ref: "refs/heads/main"},
				{URL: "git@github.com:org/repo2.git", Ref: "refs/heads/stable"},
			},
			event: makeEvent("org/repo2", "https://github.com/org/repo2.git", "git@github.com:org/repo2.git", "refs/heads/stable"),
			want:  true,
		},
		{
			name: "multi repo correct repo wrong ref",
			repos: []config.RepoSpec{
				{URL: "https://github.com/org/repo1.git", Ref: "refs/heads/main"},
				{URL: "git@github.com:org/repo2.git", Ref: "refs/heads/stable"},
			},
			event: makeEvent("org/repo2", "https://github.com/org/repo2.git", "git@github.com:org/repo2.git", "refs/heads/main"),
			want:  false,
		},
		{
			name:  "no repos configured",
			repos: nil,
			event: makeEvent("org/repo", "https://github.com/org/repo.git", "git@github.com:org/repo.git", "refs/heads/main"),
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg.Repository = nil
			cfg.Repositories = tt.repos

			mockGit := &mockGitClient{}
			mockSys := &mockSystemd{}

			server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
			if err != nil {
				t.Fatalf("NewServer() failed: %v", err)
			}

			got := server.matchesConfiguredRepo(tt.event)
			if got != tt.want {
				t.Errorf("matchesConfiguredRepo() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleWebhook_UnconfiguredRepo(t *testing.T) {
	cfg, secret := setupTestConfig(t)
	// Override to multi-repo with a different repo than the event sends
	cfg.Repository = nil
	cfg.Repositories = []config.RepoSpec{
		{URL: "https://github.com/org/other.git", Ref: "refs/heads/main"},
	}
	cfg.Serve.AllowedRefs = []string{} // disable global ref filter

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	body := []byte(`{
		"ref": "refs/heads/main",
		"after": "abc123",
		"repository": {
			"full_name": "org/repo",
			"clone_url": "https://github.com/org/repo.git",
			"ssh_url": "git@github.com:org/repo.git"
		}
	}`)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", computeSignature(body, secret))

	rec := httptest.NewRecorder()
	server.handleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Repository/ref not configured")) {
		t.Errorf("expected 'Repository/ref not configured' message, got: %s", rec.Body.String())
	}
}

func TestHandleWebhook_MultiRepo_MatchesSecondRepo(t *testing.T) {
	cfg, secret := setupTestConfig(t)
	cfg.Repository = nil
	cfg.Repositories = []config.RepoSpec{
		{URL: "https://github.com/org/repo1.git", Ref: "refs/heads/main"},
		{URL: "git@github.com:org/repo2.git", Ref: "refs/heads/stable"},
	}
	cfg.Serve.AllowedRefs = []string{} // disable global ref filter

	if err := os.MkdirAll(cfg.Paths.QuadletDir, 0755); err != nil {
		t.Fatalf("failed to create quadlet dir: %v", err)
	}
	if err := os.MkdirAll(cfg.Paths.StateDir, 0755); err != nil {
		t.Fatalf("failed to create state dir: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	body := []byte(`{
		"ref": "refs/heads/stable",
		"after": "def456",
		"repository": {
			"full_name": "org/repo2",
			"clone_url": "https://github.com/org/repo2.git",
			"ssh_url": "git@github.com:org/repo2.git"
		}
	}`)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", computeSignature(body, secret))

	rec := httptest.NewRecorder()
	server.handleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Sync triggered")) {
		t.Errorf("expected 'Sync triggered' message, got: %s", rec.Body.String())
	}
}

// makeEvent constructs a GitHubPushEvent for testing.
func makeEvent(fullName, cloneURL, sshURL, ref string) GitHubPushEvent {
	var e GitHubPushEvent
	e.Ref = ref
	e.After = "abc123"
	e.Repository.FullName = fullName
	e.Repository.CloneURL = cloneURL
	e.Repository.SSHURL = sshURL
	return e
}

// TestHandleRoot verifies the root path returns HTML for the Web UI SPA.
func TestHandleRoot(t *testing.T) {
	cfg, _ := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	tests := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
		checkBody      bool
		bodyContains   string
	}{
		{
			name:           "GET / returns 200 with HTML",
			method:         http.MethodGet,
			path:           "/",
			expectedStatus: http.StatusOK,
			checkBody:      true,
			bodyContains:   "quadsyncd",
		},
		{
			name:           "HEAD / returns 200",
			method:         http.MethodHead,
			path:           "/",
			expectedStatus: http.StatusOK,
			checkBody:      false,
		},
		{
			name:           "POST / returns 405",
			method:         http.MethodPost,
			path:           "/",
			expectedStatus: http.StatusMethodNotAllowed,
			checkBody:      false,
		},
		{
			name:           "unknown path returns 200 with SPA index",
			method:         http.MethodGet,
			path:           "/unknown",
			expectedStatus: http.StatusOK,
			checkBody:      true,
			bodyContains:   "quadsyncd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			server.handleRoot(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rec.Code)
			}

			if tt.checkBody {
				if !bytes.Contains(rec.Body.Bytes(), []byte(tt.bodyContains)) {
					t.Errorf("expected body to contain %q, got: %s", tt.bodyContains, rec.Body.String())
				}
				contentType := rec.Header().Get("Content-Type")
				if !bytes.Contains([]byte(contentType), []byte("text/html")) {
					t.Errorf("expected Content-Type to contain text/html, got: %s", contentType)
				}
			}
		})
	}
}

// TestHandleAssets verifies the /assets/* path serves embedded static assets.
func TestHandleAssets(t *testing.T) {
	cfg, _ := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	tests := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
	}{
		{
			name:           "GET /assets/nonexistent returns 404",
			method:         http.MethodGet,
			path:           "/assets/nonexistent.js",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "POST /assets/app.js returns 405",
			method:         http.MethodPost,
			path:           "/assets/app.js",
			expectedStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			server.handleAssets(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rec.Code)
			}
		})
	}
}

// TestHandleAPI verifies the /api/* path returns 501 Not Implemented.
func TestHandleAPI(t *testing.T) {
	cfg, _ := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	tests := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
		checkBody      bool
	}{
		{
			name:           "GET /api/status returns 501",
			method:         http.MethodGet,
			path:           "/api/status",
			expectedStatus: http.StatusNotImplemented,
			checkBody:      true,
		},
		{
			name:           "POST /api/sync returns 501",
			method:         http.MethodPost,
			path:           "/api/sync",
			expectedStatus: http.StatusNotImplemented,
			checkBody:      true,
		},
		{
			name:           "GET /api/repos returns 501",
			method:         http.MethodGet,
			path:           "/api/repos",
			expectedStatus: http.StatusNotImplemented,
			checkBody:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			server.handleAPI(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rec.Code)
			}

			if tt.checkBody {
				if !bytes.Contains(rec.Body.Bytes(), []byte("not implemented")) {
					t.Errorf("expected body to mention 'not implemented', got: %s", rec.Body.String())
				}
				contentType := rec.Header().Get("Content-Type")
				if contentType != "application/json" {
					t.Errorf("expected Content-Type application/json, got: %s", contentType)
				}
			}
		})
	}
}

func TestHandlePlan(t *testing.T) {
	cfg, _ := setupTestConfig(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Create test directories
	if err := os.MkdirAll(cfg.Paths.QuadletDir, 0755); err != nil {
		t.Fatalf("failed to create quadlet dir: %v", err)
	}
	if err := os.MkdirAll(cfg.Paths.StateDir, 0755); err != nil {
		t.Fatalf("failed to create state dir: %v", err)
	}

	mockGit := &mockGitClient{}
	mockSys := &mockSystemd{}

	server, err := NewServer(cfg, mockGitFactory(mockGit), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}

	t.Run("POST /api/plan creates plan run", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/plan", nil)
		w := httptest.NewRecorder()

		server.handlePlan(w, req)

		// Should return 200 or 500 depending on sync success
		// (in tests it will likely fail due to missing repo, but that's ok)
		if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
			t.Errorf("expected status 200 or 500, got %d", w.Code)
		}

		// Response should be JSON with run_id
		var resp map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if runID, ok := resp["run_id"].(string); !ok || runID == "" {
			t.Errorf("expected run_id in response, got %v", resp)
		}

		// Verify run record was created
		store := runstore.NewStore(cfg.Paths.StateDir, logger)
		runs, err := store.List(context.Background())
		if err != nil {
			t.Fatalf("failed to list runs: %v", err)
		}

		if len(runs) == 0 {
			t.Fatal("expected at least one run record")
		}

		// Check that the latest run is a plan
		latestRun := runs[0]
		if latestRun.Kind != runstore.RunKindPlan {
			t.Errorf("expected run kind 'plan', got %s", latestRun.Kind)
		}
		if latestRun.Trigger != runstore.TriggerUI {
			t.Errorf("expected trigger 'ui', got %s", latestRun.Trigger)
		}
		if !latestRun.DryRun {
			t.Error("expected dry_run to be true for plan")
		}
	})

	t.Run("POST /api/plan successful dry-run with plan.json", func(t *testing.T) {
		// Create a new server with a mock that sets up a quadlet repo
		mockGitWithRepo := &mockGitClient{
			commit: "test-commit-sha",
			repoSetup: func(destDir string) {
				// Create a minimal quadlet structure
				quadletSubdir := filepath.Join(destDir, cfg.Repository.Subdir)
				if err := os.MkdirAll(quadletSubdir, 0755); err != nil {
					t.Fatalf("failed to create quadlet subdir: %v", err)
				}
				// Write a sample .container file
				containerFile := filepath.Join(quadletSubdir, "test-app.container")
				content := `[Container]
Image=docker.io/library/nginx:latest
PublishPort=8080:80

[Service]
Restart=always
`
				if err := os.WriteFile(containerFile, []byte(content), 0644); err != nil {
					t.Fatalf("failed to write quadlet file: %v", err)
				}
			},
		}

		serverWithRepo, err := NewServer(cfg, mockGitFactory(mockGitWithRepo), mockSys, runstore.NewStore(cfg.Paths.StateDir, logger), logger)
		if err != nil {
			t.Fatalf("NewServer() failed: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/api/plan", nil)
		w := httptest.NewRecorder()

		serverWithRepo.handlePlan(w, req)

		// Should return 200 on successful plan
		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}

		// Response should be JSON with run_id and success status
		var resp map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		runID, ok := resp["run_id"].(string)
		if !ok || runID == "" {
			t.Errorf("expected run_id in response, got %v", resp)
		}

		status, ok := resp["status"].(string)
		if !ok || status != "success" {
			t.Errorf("expected status 'success', got %v", resp)
		}

		// Verify plan.json was created and can be read
		store := runstore.NewStore(cfg.Paths.StateDir, logger)
		plan, err := store.ReadPlan(context.Background(), runID)
		if err != nil {
			t.Fatalf("failed to read plan.json: %v", err)
		}

		// Validate plan structure
		if len(plan.Ops) == 0 {
			t.Error("expected at least one operation in plan")
		}

		// Verify we have an 'add' operation for the test-app.container
		foundAdd := false
		for _, op := range plan.Ops {
			if op.Op == "add" && strings.Contains(op.Path, "test-app.container") {
				foundAdd = true
				if op.SourceRepo != cfg.Repository.URL {
					t.Errorf("expected source_repo %s, got %s", cfg.Repository.URL, op.SourceRepo)
				}
				if op.SourceRef != cfg.Repository.Ref {
					t.Errorf("expected source_ref %s, got %s", cfg.Repository.Ref, op.SourceRef)
				}
				if op.SourceSHA != "test-commit-sha" {
					t.Errorf("expected source_sha 'test-commit-sha', got %s", op.SourceSHA)
				}
			}
		}

		if !foundAdd {
			t.Error("expected to find 'add' operation for test-app.container in plan")
		}
	})

	t.Run("GET /api/plan returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/plan", nil)
		w := httptest.NewRecorder()

		server.handlePlan(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", w.Code)
		}
	})
}

// ---- helpers for new API tests ----

// setupServerWithRuns creates a server and pre-populates the runstore
// with the given run metas (stored as-is, not executed).
func setupServerWithRuns(t *testing.T, runs []runstore.RunMeta) (*Server, *runstore.Store) {
	t.Helper()
	cfg, _ := setupTestConfig(t)

	if err := os.MkdirAll(cfg.Paths.QuadletDir, 0755); err != nil {
		t.Fatalf("MkdirAll quadlet: %v", err)
	}
	if err := os.MkdirAll(cfg.Paths.StateDir, 0755); err != nil {
		t.Fatalf("MkdirAll state: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	store := runstore.NewStore(cfg.Paths.StateDir, logger)

	ctx := context.Background()
	for i := range runs {
		if err := store.Create(ctx, &runs[i]); err != nil {
			t.Fatalf("store.Create: %v", err)
		}
		if err := store.Update(ctx, &runs[i]); err != nil {
			t.Fatalf("store.Update: %v", err)
		}
	}

	server, err := NewServer(cfg, mockGitFactory(&mockGitClient{}), &mockSystemd{}, store, logger)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return server, store
}

func makeRun(kind runstore.RunKind, trigger runstore.TriggerSource) runstore.RunMeta {
	now := time.Now().UTC()
	ended := now.Add(2 * time.Second)
	return runstore.RunMeta{
		Kind:      kind,
		Trigger:   trigger,
		StartedAt: now,
		EndedAt:   &ended,
		Status:    runstore.RunStatusSuccess,
		DryRun:    kind == runstore.RunKindPlan,
		Revisions: map[string]string{"https://github.com/test/repo.git": "abc123"},
		Conflicts: []runstore.ConflictSummary{},
	}
}

func requireJSONContentType(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

// ---- GET /api/overview ----

func TestHandleOverview(t *testing.T) {
	run := makeRun(runstore.RunKindSync, runstore.TriggerTimer)
	server, _ := setupServerWithRuns(t, []runstore.RunMeta{run})

	t.Run("returns 200 with expected fields", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		requireJSONContentType(t, w)

		var resp OverviewResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Repositories) == 0 {
			t.Error("expected at least one repository")
		}
		if resp.Repositories[0].URL == "" {
			t.Error("expected repository URL")
		}
		if resp.Repositories[0].Ref == "" {
			t.Error("expected repository Ref")
		}
		if resp.LastRunID == "" {
			t.Error("expected last_run_id")
		}
		if resp.LastRunStatus != string(runstore.RunStatusSuccess) {
			t.Errorf("expected last_run_status success, got %q", resp.LastRunStatus)
		}
	})

	t.Run("POST returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/overview", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", w.Code)
		}
	})

	t.Run("returns 200 with empty runs", func(t *testing.T) {
		emptyServer, _ := setupServerWithRuns(t, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
		w := httptest.NewRecorder()
		emptyServer.handleAPI(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp OverviewResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.LastRunID != "" {
			t.Error("expected no last_run_id when no runs exist")
		}
	})
}

// ---- GET /api/runs ----

func TestHandleRuns(t *testing.T) {
	runs := []runstore.RunMeta{
		makeRun(runstore.RunKindSync, runstore.TriggerTimer),
		makeRun(runstore.RunKindPlan, runstore.TriggerUI),
	}
	server, _ := setupServerWithRuns(t, runs)

	t.Run("returns 200 with items array", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		requireJSONContentType(t, w)

		var resp RunsListResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Items) != 2 {
			t.Errorf("expected 2 items, got %d", len(resp.Items))
		}
	})

	t.Run("pagination with limit=1", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs?limit=1", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp RunsListResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Items) != 1 {
			t.Errorf("expected 1 item, got %d", len(resp.Items))
		}
		if resp.NextCursor == "" {
			t.Error("expected next_cursor when more items exist")
		}

		// Follow cursor
		req2 := httptest.NewRequest(http.MethodGet, "/api/runs?limit=1&cursor="+resp.NextCursor, nil)
		w2 := httptest.NewRecorder()
		server.handleAPI(w2, req2)
		if w2.Code != http.StatusOK {
			t.Fatalf("cursor page: expected 200, got %d", w2.Code)
		}
		var resp2 RunsListResponse
		if err := json.NewDecoder(w2.Body).Decode(&resp2); err != nil {
			t.Fatalf("decode page 2: %v", err)
		}
		if len(resp2.Items) != 1 {
			t.Errorf("expected 1 item on page 2, got %d", len(resp2.Items))
		}
		if resp2.NextCursor != "" {
			t.Error("expected no next_cursor on last page")
		}
	})

	t.Run("POST returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/runs", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", w.Code)
		}
	})

	t.Run("empty store returns empty items array", func(t *testing.T) {
		emptyServer, _ := setupServerWithRuns(t, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
		w := httptest.NewRecorder()
		emptyServer.handleAPI(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp RunsListResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Items == nil {
			t.Error("items should not be null")
		}
	})

	t.Run("invalid cursor treated as offset 0", func(t *testing.T) {
		baseReq := httptest.NewRequest(http.MethodGet, "/api/runs?limit=1", nil)
		baseW := httptest.NewRecorder()
		server.handleAPI(baseW, baseReq)
		if baseW.Code != http.StatusOK {
			t.Fatalf("baseline: expected 200, got %d", baseW.Code)
		}

		invalidReq := httptest.NewRequest(http.MethodGet, "/api/runs?limit=1&cursor=not-valid-base64!!", nil)
		invalidW := httptest.NewRecorder()
		server.handleAPI(invalidW, invalidReq)
		if invalidW.Code != http.StatusOK {
			t.Fatalf("invalid cursor: expected 200, got %d", invalidW.Code)
		}

		if !bytes.Equal(baseW.Body.Bytes(), invalidW.Body.Bytes()) {
			t.Errorf("invalid cursor should behave as offset 0;\nbase=%s\ninvalid=%s",
				baseW.Body.String(), invalidW.Body.String())
		}
	})

	t.Run("limit exceeding max is clamped to 100", func(t *testing.T) {
		var manyRuns []runstore.RunMeta
		for i := 0; i < 150; i++ {
			manyRuns = append(manyRuns, makeRun(runstore.RunKindSync, runstore.TriggerWebhook))
		}
		bigServer, _ := setupServerWithRuns(t, manyRuns)

		req := httptest.NewRequest(http.MethodGet, "/api/runs?limit=200", nil)
		w := httptest.NewRecorder()
		bigServer.handleAPI(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp RunsListResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Items) != 100 {
			t.Errorf("expected 100 items (clamped), got %d", len(resp.Items))
		}
	})

	t.Run("negative limit falls back to default", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs?limit=-5", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp RunsListResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		// 2 runs seeded; default limit (20) >= 2, so all are returned.
		if len(resp.Items) != 2 {
			t.Errorf("expected 2 items with negative limit ignored, got %d", len(resp.Items))
		}
	})

	t.Run("non-numeric limit falls back to default", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs?limit=not-a-number", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp RunsListResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Items) != 2 {
			t.Errorf("expected 2 items with non-numeric limit ignored, got %d", len(resp.Items))
		}
	})
}

// ---- GET /api/runs/{id} ----

func TestHandleRunDetail(t *testing.T) {
	run := makeRun(runstore.RunKindSync, runstore.TriggerWebhook)
	server, store := setupServerWithRuns(t, []runstore.RunMeta{run})

	// Retrieve the actual ID assigned by the store.
	ctx := context.Background()
	allRuns, _ := store.List(ctx)
	runID := allRuns[0].ID

	t.Run("returns full run meta", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID, nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		requireJSONContentType(t, w)

		var meta runstore.RunMeta
		if err := json.NewDecoder(w.Body).Decode(&meta); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if meta.ID != runID {
			t.Errorf("expected ID %q, got %q", runID, meta.ID)
		}
		if meta.Kind != runstore.RunKindSync {
			t.Errorf("expected kind sync, got %s", meta.Kind)
		}
		if meta.Status != runstore.RunStatusSuccess {
			t.Errorf("expected status success, got %s", meta.Status)
		}
	})

	t.Run("returns 404 for unknown run", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs/nonexistent-run-id", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Code)
		}
		requireJSONContentType(t, w)
	})

	t.Run("POST returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/runs/"+runID, nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", w.Code)
		}
	})
}

// ---- GET /api/runs/{id}/logs ----

func TestHandleRunLogs(t *testing.T) {
	run := makeRun(runstore.RunKindSync, runstore.TriggerTimer)
	server, store := setupServerWithRuns(t, []runstore.RunMeta{run})

	ctx := context.Background()
	allRuns, _ := store.List(ctx)
	runID := allRuns[0].ID

	// Write some log lines.
	logLines := []string{
		`{"time":"2026-01-01T10:00:00Z","level":"INFO","msg":"starting sync","component":"engine"}`,
		`{"time":"2026-01-01T10:00:01Z","level":"DEBUG","msg":"checking files","component":"engine"}`,
		`{"time":"2026-01-01T10:00:02Z","level":"ERROR","msg":"something failed","component":"git"}`,
		`{"time":"2026-01-01T10:00:03Z","level":"INFO","msg":"sync complete","component":"engine"}`,
	}
	for _, line := range logLines {
		if err := store.AppendLog(ctx, runID, []byte(line)); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}

	t.Run("returns all logs", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/logs", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		requireJSONContentType(t, w)

		var resp RunLogsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Items) != 4 {
			t.Errorf("expected 4 log records, got %d", len(resp.Items))
		}
	})

	t.Run("filter by level=ERROR", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/logs?level=error", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp RunLogsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Items) != 1 {
			t.Errorf("expected 1 ERROR record, got %d", len(resp.Items))
		}
		if msg, _ := resp.Items[0]["msg"].(string); msg != "something failed" {
			t.Errorf("unexpected msg %q", msg)
		}
	})

	t.Run("filter by component=git", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/logs?component=git", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp RunLogsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Items) != 1 {
			t.Errorf("expected 1 git record, got %d", len(resp.Items))
		}
	})

	t.Run("filter by q=complete", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/logs?q=complete", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp RunLogsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Items) != 1 {
			t.Errorf("expected 1 record matching 'complete', got %d", len(resp.Items))
		}
	})

	t.Run("filter by since", func(t *testing.T) {
		// since=2026-01-01T10:00:01Z should return records strictly after that time
		req := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/logs?since=2026-01-01T10:00:01Z", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp RunLogsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		// records at 10:00:02 and 10:00:03 are after the since time
		if len(resp.Items) != 2 {
			t.Errorf("expected 2 records after since, got %d", len(resp.Items))
		}
	})

	t.Run("pagination with limit=2", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/logs?limit=2", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		var resp RunLogsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Items) != 2 {
			t.Errorf("expected 2 items on first page, got %d", len(resp.Items))
		}
		if resp.NextCursor == "" {
			t.Error("expected next_cursor")
		}
	})

	t.Run("returns 404 for unknown run", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs/nonexistent/logs", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Code)
		}
	})

	t.Run("POST returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/runs/"+runID+"/logs", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", w.Code)
		}
	})
}

// ---- GET /api/runs/{id}/plan ----

func TestHandleRunPlan(t *testing.T) {
	run := makeRun(runstore.RunKindPlan, runstore.TriggerUI)
	server, store := setupServerWithRuns(t, []runstore.RunMeta{run})

	ctx := context.Background()
	allRuns, _ := store.List(ctx)
	runID := allRuns[0].ID

	// Write a plan.
	plan := runstore.Plan{
		Requested: runstore.PlanRequest{},
		Conflicts: []runstore.ConflictSummary{},
		Ops: []runstore.PlanOp{
			{Op: "add", Path: "/home/user/.config/containers/systemd/test.container", SourceRepo: "https://github.com/test/repo.git"},
		},
	}
	if err := store.WritePlan(ctx, runID, plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	t.Run("returns plan JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/plan", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		requireJSONContentType(t, w)

		var p runstore.Plan
		if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(p.Ops) != 1 {
			t.Errorf("expected 1 op, got %d", len(p.Ops))
		}
		if p.Ops[0].Op != "add" {
			t.Errorf("expected op 'add', got %q", p.Ops[0].Op)
		}
	})

	t.Run("returns 404 when no plan exists", func(t *testing.T) {
		// Create a sync run (no plan.json).
		syncRun := makeRun(runstore.RunKindSync, runstore.TriggerTimer)
		if err := store.Create(ctx, &syncRun); err != nil {
			t.Fatalf("store.Create: %v", err)
		}
		if err := store.Update(ctx, &syncRun); err != nil {
			t.Fatalf("store.Update: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/runs/"+syncRun.ID+"/plan", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Code)
		}
	})

	t.Run("returns 404 for unknown run", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/runs/nonexistent/plan", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Code)
		}
	})

	t.Run("POST returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/runs/"+runID+"/plan", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", w.Code)
		}
	})
}

// ---- GET /api/units ----

func TestHandleUnits(t *testing.T) {
	server, _ := setupServerWithRuns(t, nil)

	t.Run("returns empty items when no state.json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/units", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		requireJSONContentType(t, w)

		var resp UnitsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Items == nil {
			t.Error("items should not be null")
		}
	})

	t.Run("returns units from state.json", func(t *testing.T) {
		cfg, _ := setupTestConfig(t)
		if err := os.MkdirAll(cfg.Paths.StateDir, 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}

		// Write a minimal state.json.
		stateContent := `{
"managed_files": {
"/home/user/.config/containers/systemd/app.container": {
"source_path": "app.container",
"hash": "abc123",
"source_repo": "https://github.com/test/repo.git",
"source_ref": "refs/heads/main",
"source_sha": "deadbeef"
}
}
}`
		if err := os.WriteFile(cfg.StateFilePath(), []byte(stateContent), 0644); err != nil {
			t.Fatalf("WriteFile state: %v", err)
		}

		logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
		store := runstore.NewStore(cfg.Paths.StateDir, logger)
		srv, err := NewServer(cfg, mockGitFactory(&mockGitClient{}), &mockSystemd{}, store, logger)
		if err != nil {
			t.Fatalf("NewServer: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/units", nil)
		w := httptest.NewRecorder()
		srv.handleAPI(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp UnitsResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Items) != 1 {
			t.Fatalf("expected 1 unit, got %d", len(resp.Items))
		}
		u := resp.Items[0]
		if u.Name != "app.service" {
			t.Errorf("expected unit name app.service, got %q", u.Name)
		}
		if u.SourceRepo != "https://github.com/test/repo.git" {
			t.Errorf("unexpected source_repo %q", u.SourceRepo)
		}
		if u.Hash != "abc123" {
			t.Errorf("unexpected hash %q", u.Hash)
		}
	})

	t.Run("POST returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/units", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", w.Code)
		}
	})
}

// ---- GET /api/timer ----

func TestHandleTimer(t *testing.T) {
	server, _ := setupServerWithRuns(t, nil)

	t.Run("returns 200 with timer unit field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/timer", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		requireJSONContentType(t, w)

		var resp TimerInfo
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Unit != "quadsyncd-sync.timer" {
			t.Errorf("expected unit quadsyncd-sync.timer, got %q", resp.Unit)
		}
		// active will be false in CI/test env; just ensure it's present (bool field)
	})

	t.Run("POST returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/timer", nil)
		w := httptest.NewRecorder()
		server.handleAPI(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", w.Code)
		}
	})
}

// ---- routing edge cases ----

func TestHandleAPIRouting(t *testing.T) {
	server, _ := setupServerWithRuns(t, nil)

	tests := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
	}{
		// Unimplemented paths still return 501
		{"unknown path", http.MethodGet, "/api/status", http.StatusNotImplemented},
		{"trailing slash on runs", http.MethodGet, "/api/runs/", http.StatusNotImplemented},
		{"deep unknown subpath", http.MethodGet, "/api/runs/abc/def/ghi", http.StatusNotImplemented},
		// Implemented paths with correct method
		{"overview", http.MethodGet, "/api/overview", http.StatusOK},
		{"runs list", http.MethodGet, "/api/runs", http.StatusOK},
		{"units", http.MethodGet, "/api/units", http.StatusOK},
		{"timer", http.MethodGet, "/api/timer", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			server.handleAPI(w, req)
			if w.Code != tt.expectedStatus {
				t.Errorf("expected %d, got %d (body: %s)", tt.expectedStatus, w.Code, w.Body.String())
			}
			requireJSONContentType(t, w)
		})
	}
}

// ---- Security headers middleware ----

func TestSecurityHeadersMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := securityHeadersMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	tests := []struct {
		header string
		want   string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"Referrer-Policy", "no-referrer"},
		{"Permissions-Policy", "interest-cohort=()"},
		{"Content-Security-Policy", cspPolicy},
	}
	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			got := w.Header().Get(tt.header)
			if got != tt.want {
				t.Errorf("%s: expected %q, got %q", tt.header, tt.want, got)
			}
		})
	}
}

func TestSecurityHeadersMiddleware_UIAndAPIRoutes(t *testing.T) {
	server, _ := setupServerWithRuns(t, nil)

	// Build the same handler chain as StartWithListener.
	mux := http.NewServeMux()
	mux.HandleFunc("/", server.handleRoot)
	mux.HandleFunc("/api/", server.handleAPI)
	handler := securityHeadersMiddleware(csrfMiddleware(mux))

	for _, path := range []string{"/", "/api/overview"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: expected X-Content-Type-Options nosniff, got %q", path, got)
		}
		if got := w.Header().Get("Content-Security-Policy"); got != cspPolicy {
			t.Errorf("%s: expected CSP header set, got %q", path, got)
		}
	}
}

// ---- CSRF middleware ----

func TestCSRFMiddleware_SetsCookieOnGetRoot(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := csrfMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var csrfCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == csrfCookieName {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("expected csrf_token cookie to be set on GET /")
	}
	if csrfCookie.HttpOnly {
		t.Error("csrf_token cookie must not be HttpOnly (JS must be able to read it)")
	}
	if csrfCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("csrf_token cookie SameSite expected Lax, got %v", csrfCookie.SameSite)
	}
	if csrfCookie.Value == "" {
		t.Error("csrf_token cookie value must not be empty")
	}
}

func TestCSRFMiddleware_RegeneratesCookieWhenEmpty(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := csrfMiddleware(inner)

	// Send an empty csrf_token cookie — middleware should regenerate it.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: ""})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var found *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == csrfCookieName {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatal("expected a new csrf_token cookie when existing cookie has empty value")
	}
	if found.Value == "" {
		t.Error("regenerated csrf_token cookie value must not be empty")
	}
}

func TestCSRFMiddleware_DoesNotSetCookieIfAlreadyPresent(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := csrfMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "existing-token"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var count int
	for _, c := range w.Result().Cookies() {
		if c.Name == csrfCookieName {
			count++
		}
	}
	// No new cookie should be set when one already exists.
	if count != 0 {
		t.Errorf("expected no new csrf_token cookie when already present, got %d", count)
	}
}

func TestCSRFMiddleware_PostMissingCookie(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := csrfMiddleware(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/plan", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 when CSRF cookie missing, got %d", w.Code)
	}
}

func TestCSRFMiddleware_PostMismatchHeader(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := csrfMiddleware(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/plan", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "real-token"})
	req.Header.Set("X-CSRF-Token", "wrong-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 on CSRF token mismatch, got %d", w.Code)
	}
}

func TestCSRFMiddleware_PostMissingHeader(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := csrfMiddleware(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/plan", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "real-token"})
	// No X-CSRF-Token header set.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 when X-CSRF-Token header missing, got %d", w.Code)
	}
}

func TestCSRFMiddleware_PostValidToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := csrfMiddleware(inner)

	token := "abc123validtoken"
	req := httptest.NewRequest(http.MethodPost, "/api/plan", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	req.Header.Set("X-CSRF-Token", token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for valid CSRF token, got %d", w.Code)
	}
}

func TestCSRFMiddleware_WebhookPathUnaffected(t *testing.T) {
	// /webhook must pass through without CSRF checks (it uses HMAC-SHA256).
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := csrfMiddleware(inner)

	// POST to /webhook without cookie or header must still reach the inner handler.
	req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected /webhook to bypass CSRF (got %d)", w.Code)
	}
}

func TestCSRFMiddleware_GetNonRootNoCSRFRequired(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := csrfMiddleware(inner)

	// GET requests to non-root paths don't need CSRF validation.
	req := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected GET /api/runs to pass through without CSRF, got %d", w.Code)
	}
}
