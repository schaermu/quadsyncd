package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestSetupLogger(t *testing.T) {
	// Save original globals.
	origLevel := logLevel
	origFormat := logFormat
	t.Cleanup(func() {
		logLevel = origLevel
		logFormat = origFormat
	})

	for _, tc := range []struct {
		name      string
		logLevel  string
		logFormat string
	}{
		{name: "debug/text", logLevel: "debug", logFormat: "text"},
		{name: "info/json", logLevel: "info", logFormat: "json"},
		{name: "warn/text", logLevel: "warn", logFormat: "text"},
		{name: "error/text", logLevel: "error", logFormat: "text"},
		{name: "unknown/text", logLevel: "unknown", logFormat: "text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logLevel = tc.logLevel
			logFormat = tc.logFormat

			logger := setupLogger()
			if logger == nil {
				t.Fatal("setupLogger returned nil")
			}
		})
	}
}

func TestLoadConfig_WithExplicitPath(t *testing.T) {
	origCfgFile := cfgFile
	t.Cleanup(func() { cfgFile = origCfgFile })

	tmpDir := t.TempDir()
	quadletDir := filepath.Join(tmpDir, "quadlets")
	stateDir := filepath.Join(tmpDir, "state")

	configContent := []byte(`repo:
  url: "git@github.com:test/repo.git"
  ref: "refs/heads/main"
  subdir: "quadlets"
paths:
  quadlet_dir: "` + quadletDir + `"
  state_dir: "` + stateDir + `"
sync:
  prune: true
  restart: "changed"
auth:
  ssh_key_file: ""
`)
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, configContent, 0o600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfgFile = cfgPath
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg, err := loadConfig(logger)
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("loadConfig returned nil config")
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	origCfgFile := cfgFile
	t.Cleanup(func() { cfgFile = origCfgFile })

	cfgFile = filepath.Join(t.TempDir(), "nonexistent.yaml")
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	_, err := loadConfig(logger)
	if err == nil {
		t.Fatal("expected error for missing config file, got nil")
	}
}

func TestSetupSignalHandler(t *testing.T) {
	ctx, cancel := setupSignalHandler()
	if ctx == nil {
		t.Fatal("setupSignalHandler returned nil context")
	}

	cancel()

	<-ctx.Done()
	if err := ctx.Err(); err == nil {
		t.Fatal("expected context error after cancel, got nil")
	}
}

func TestLoadConfig_DefaultPath(t *testing.T) {
	origCfgFile := cfgFile
	defer func() { cfgFile = origCfgFile }()
	cfgFile = ""
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	_, err := loadConfig(logger)
	// Expect error because the default config file doesn't exist
	if err == nil {
		t.Error("expected error when default config file doesn't exist")
	}
}

func TestVersionCmd(t *testing.T) {
	t.Helper()
	// versionCmd.Run simply prints version info; should not panic.
	versionCmd.Run(versionCmd, []string{})
}
