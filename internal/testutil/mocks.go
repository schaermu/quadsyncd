package testutil

import (
	"context"
	"fmt"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/git"
)

// MockGitClient implements git.Client for testing.
type MockGitClient struct {
	CommitHash string
	Err        error
	Called     bool
	RepoSetup  func(destDir string)
}

func (m *MockGitClient) EnsureCheckout(_ context.Context, _, _, destDir string) (string, error) {
	m.Called = true
	if m.RepoSetup != nil {
		m.RepoSetup(destDir)
	}
	return m.CommitHash, m.Err
}

// MockSystemd implements systemduser.Systemd for testing.
type MockSystemd struct {
	Available      bool
	AvailableErr   error
	ReloadErr      error
	RestartErr     error
	ValidateErr    error
	ReloadCalled   bool
	RestartCalled  bool
	ValidateCalled bool
	RestartedUnits []string
}

func (m *MockSystemd) IsAvailable(_ context.Context) (bool, error) {
	return m.Available, m.AvailableErr
}

func (m *MockSystemd) DaemonReload(_ context.Context) error {
	m.ReloadCalled = true
	return m.ReloadErr
}

func (m *MockSystemd) TryRestartUnits(_ context.Context, units []string) error {
	m.RestartCalled = true
	m.RestartedUnits = units
	return m.RestartErr
}

func (m *MockSystemd) ValidateQuadlets(_ context.Context, _ string) error {
	m.ValidateCalled = true
	return m.ValidateErr
}

func (m *MockSystemd) GetUnitStatus(_ context.Context, _ string) (string, error) {
	return "inactive", nil
}

// MultiMockGitClient routes EnsureCheckout calls to per-URL MockGitClient handlers.
type MultiMockGitClient struct {
	Handlers map[string]*MockGitClient
}

func (m *MultiMockGitClient) EnsureCheckout(ctx context.Context, url, ref, destDir string) (string, error) {
	if h, ok := m.Handlers[url]; ok {
		return h.EnsureCheckout(ctx, url, ref, destDir)
	}
	return "", fmt.Errorf("no handler for URL %q", url)
}

// MockGitFactory returns a GitClientFactory (as a plain function) that always
// returns the given MockGitClient. The return type is the underlying function
// type, which is assignable to both sync.GitClientFactory and
// webhook.GitClientFactory without an explicit conversion.
func MockGitFactory(mock *MockGitClient) func(config.AuthConfig) git.Client {
	return func(_ config.AuthConfig) git.Client { return mock }
}
