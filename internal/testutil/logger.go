package testutil

import (
	"log/slog"
	"os"
)

// TestLogger returns a logger suitable for tests: error-level only, writing to os.Stderr.
func TestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}
