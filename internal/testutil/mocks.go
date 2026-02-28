package testutil

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/git"
	"github.com/schaermu/quadsyncd/internal/runstore"
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

// MockRunStore implements runstore.ReadWriter for testing.
// Provides in-memory default implementations; override individual Func fields
// to inject errors or custom behaviour in specific tests.
type MockRunStore struct {
	mu        sync.Mutex
	Runs      map[string]*runstore.RunMeta
	Plans     map[string]*runstore.Plan
	Logs      map[string][][]byte
	Artifacts map[string]map[string][]byte

	CreateFunc        func(ctx context.Context, meta *runstore.RunMeta) error
	UpdateFunc        func(ctx context.Context, meta *runstore.RunMeta) error
	GetFunc           func(ctx context.Context, id string) (*runstore.RunMeta, error)
	ListFunc          func(ctx context.Context) ([]runstore.RunMeta, error)
	AppendLogFunc     func(ctx context.Context, id string, line []byte) error
	ReadLogFunc       func(ctx context.Context, id string) ([]map[string]interface{}, error)
	WritePlanFunc     func(ctx context.Context, id string, plan runstore.Plan) error
	ReadPlanFunc      func(ctx context.Context, id string) (*runstore.Plan, error)
	WriteArtifactFunc func(ctx context.Context, id, name string, content []byte) error
	ReadArtifactFunc  func(ctx context.Context, id, name string) ([]byte, error)
	WorkDirForRunFunc func(id string) (string, error)
	PruneFunc         func(ctx context.Context, maxAge time.Duration) error
}

// NewMockRunStore returns a MockRunStore with empty in-memory maps initialised.
func NewMockRunStore() *MockRunStore {
	return &MockRunStore{
		Runs:      make(map[string]*runstore.RunMeta),
		Plans:     make(map[string]*runstore.Plan),
		Logs:      make(map[string][][]byte),
		Artifacts: make(map[string]map[string][]byte),
	}
}

// Compile-time check that *MockRunStore satisfies runstore.ReadWriter.
var _ runstore.ReadWriter = (*MockRunStore)(nil)

func (m *MockRunStore) Create(ctx context.Context, meta *runstore.RunMeta) error {
	if m.CreateFunc != nil {
		return m.CreateFunc(ctx, meta)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if meta.ID == "" {
		meta.ID = fmt.Sprintf("run-%d", len(m.Runs)+1)
	}
	cp := *meta
	m.Runs[meta.ID] = &cp
	return nil
}

func (m *MockRunStore) Update(ctx context.Context, meta *runstore.RunMeta) error {
	if m.UpdateFunc != nil {
		return m.UpdateFunc(ctx, meta)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.Runs[meta.ID]; !ok {
		return fmt.Errorf("%w: %s", runstore.ErrRunNotFound, meta.ID)
	}
	cp := *meta
	m.Runs[meta.ID] = &cp
	return nil
}

func (m *MockRunStore) Get(ctx context.Context, id string) (*runstore.RunMeta, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.Runs[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", runstore.ErrRunNotFound, id)
	}
	cp := *r
	return &cp, nil
}

func (m *MockRunStore) List(ctx context.Context) ([]runstore.RunMeta, error) {
	if m.ListFunc != nil {
		return m.ListFunc(ctx)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]runstore.RunMeta, 0, len(m.Runs))
	for _, r := range m.Runs {
		out = append(out, *r)
	}
	return out, nil
}

func (m *MockRunStore) AppendLog(ctx context.Context, id string, line []byte) error {
	if m.AppendLogFunc != nil {
		return m.AppendLogFunc(ctx, id, line)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(line))
	copy(cp, line)
	m.Logs[id] = append(m.Logs[id], cp)
	return nil
}

func (m *MockRunStore) ReadLog(ctx context.Context, id string) ([]map[string]interface{}, error) {
	if m.ReadLogFunc != nil {
		return m.ReadLogFunc(ctx, id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return []map[string]interface{}{}, nil
}

func (m *MockRunStore) WritePlan(ctx context.Context, id string, plan runstore.Plan) error {
	if m.WritePlanFunc != nil {
		return m.WritePlanFunc(ctx, id, plan)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := plan
	m.Plans[id] = &cp
	return nil
}

func (m *MockRunStore) ReadPlan(ctx context.Context, id string) (*runstore.Plan, error) {
	if m.ReadPlanFunc != nil {
		return m.ReadPlanFunc(ctx, id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.Plans[id]
	if !ok {
		return nil, fmt.Errorf("%w for run: %s", runstore.ErrPlanNotFound, id)
	}
	cp := *p
	return &cp, nil
}

func (m *MockRunStore) WriteArtifact(ctx context.Context, id, name string, content []byte) error {
	if m.WriteArtifactFunc != nil {
		return m.WriteArtifactFunc(ctx, id, name, content)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Artifacts[id] == nil {
		m.Artifacts[id] = make(map[string][]byte)
	}
	cp := make([]byte, len(content))
	copy(cp, content)
	m.Artifacts[id][name] = cp
	return nil
}

func (m *MockRunStore) ReadArtifact(ctx context.Context, id, name string) ([]byte, error) {
	if m.ReadArtifactFunc != nil {
		return m.ReadArtifactFunc(ctx, id, name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if artifacts, ok := m.Artifacts[id]; ok {
		if data, ok := artifacts[name]; ok {
			cp := make([]byte, len(data))
			copy(cp, data)
			return cp, nil
		}
	}
	return nil, fmt.Errorf("artifact not found: %s", name)
}

func (m *MockRunStore) WorkDirForRun(id string) (string, error) {
	if m.WorkDirForRunFunc != nil {
		return m.WorkDirForRunFunc(id)
	}
	return "/tmp/workdir-" + id, nil
}

func (m *MockRunStore) Prune(ctx context.Context, maxAge time.Duration) error {
	if m.PruneFunc != nil {
		return m.PruneFunc(ctx, maxAge)
	}
	return nil
}
