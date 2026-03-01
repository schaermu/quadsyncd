package systemduser

import (
	"context"
	"log/slog"
	"os"
	"testing"
)

// testLogger returns a discard logger suitable for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNewClient(t *testing.T) {
	c := NewClient(testLogger())
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
}

func TestTryRestartUnits_Empty(t *testing.T) {
	c := NewClient(testLogger())
	err := c.TryRestartUnits(context.Background(), []string{})
	if err != nil {
		t.Fatalf("TryRestartUnits with empty slice returned error: %v", err)
	}
}

func TestRestartUnits_Empty(t *testing.T) {
	c := NewClient(testLogger())
	err := c.RestartUnits(context.Background(), []string{})
	if err != nil {
		t.Fatalf("RestartUnits with empty slice returned error: %v", err)
	}
}
