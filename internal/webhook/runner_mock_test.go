package webhook

import (
	"context"
	"log/slog"

	"github.com/schaermu/quadsyncd/internal/config"
	quadsyncd "github.com/schaermu/quadsyncd/internal/sync"
)

// mockRunner implements sync.Runner for testing.
type mockRunner struct {
	result *quadsyncd.Result
	err    error
}

func (m *mockRunner) Run(_ context.Context) (*quadsyncd.Result, error) {
	return m.result, m.err
}

// mockRunnerFactory returns a RunnerFactory that always returns the given runner.
func mockRunnerFactory(runner quadsyncd.Runner) quadsyncd.RunnerFactory {
	return func(_ *config.Config, _ *slog.Logger, _ bool, _ *quadsyncd.PlanEngineOptions) quadsyncd.Runner {
		return runner
	}
}
