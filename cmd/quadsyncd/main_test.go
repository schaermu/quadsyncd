package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

	configContent := []byte(`repository:
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
	origHome := os.Getenv("HOME")
	// Point HOME to an empty temp dir so the default config path doesn't exist.
	t.Setenv("HOME", t.TempDir())
	defer func() {
		cfgFile = origCfgFile
		os.Setenv("HOME", origHome) //nolint:errcheck
	}()
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

// writeTempConfig writes a minimal but valid quadsyncd config to a temp file
// and returns the path. The quadlet_dir and state_dir point into tmpDir.
func writeTempConfig(t *testing.T, tmpDir string) string {
	t.Helper()
	quadletDir := filepath.Join(tmpDir, "quadlets")
	stateDir := filepath.Join(tmpDir, "state")
	content := `repository:
  url: "https://github.com/test/repo.git"
  ref: "refs/heads/main"
paths:
  quadlet_dir: "` + quadletDir + `"
  state_dir: "` + stateDir + `"
sync:
  prune: false
  restart: "none"
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("writeTempConfig: %v", err)
	}
	return cfgPath
}

// TestCLI_InvalidConfigPath_ExitsNonZero verifies that supplying a non-existent
// config path causes the sync command to return a non-nil error.
func TestCLI_InvalidConfigPath_ExitsNonZero(t *testing.T) {
	origCfg := cfgFile
	t.Cleanup(func() { cfgFile = origCfg })

	cfgFile = filepath.Join(t.TempDir(), "does-not-exist.yaml")

	rootCmd.SetArgs([]string{"sync"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected non-nil error for missing config path, got nil")
	}
	if !strings.Contains(err.Error(), "config") && !strings.Contains(err.Error(), "no such file") {
		t.Errorf("expected error to mention config or file, got: %v", err)
	}
}

// TestCLI_Sync_LogsStarting verifies that the sync command emits a structured
// log entry containing "starting sync operation" before failing on git access.
func TestCLI_Sync_LogsStarting(t *testing.T) {
	origCfg := cfgFile
	origLevel := logLevel
	origFormat := logFormat
	t.Cleanup(func() {
		cfgFile = origCfg
		logLevel = origLevel
		logFormat = origFormat
	})

	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "quadlets"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}

	cfgFile = writeTempConfig(t, tmpDir)
	logFormat = "json"
	logLevel = "info"

	var buf bytes.Buffer
	// Redirect stdout so we capture the JSON log output.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	rootCmd.SetArgs([]string{"sync"})
	_ = rootCmd.Execute() // will fail at git; we only care about the log line

	_ = w.Close()
	os.Stdout = origStdout
	_, _ = buf.ReadFrom(r)

	output := buf.String()
	if !strings.Contains(output, "starting sync operation") {
		t.Errorf("expected 'starting sync operation' in log output, got:\n%s", output)
	}
}

// TestCLI_Plan_DryRunFlag verifies that passing --dry-run sets the dryRun flag
// and that the sync command acknowledges it in logs.
func TestCLI_Plan_DryRunFlag(t *testing.T) {
	origCfg := cfgFile
	origDryRun := dryRun
	origFormat := logFormat
	origLevel := logLevel
	t.Cleanup(func() {
		cfgFile = origCfg
		dryRun = origDryRun
		logFormat = origFormat
		logLevel = origLevel
	})

	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "quadlets"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}

	cfgFile = writeTempConfig(t, tmpDir)
	logFormat = "json"
	logLevel = "info"

	var buf bytes.Buffer
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	rootCmd.SetArgs([]string{"sync", "--dry-run"})
	_ = rootCmd.Execute()

	_ = w.Close()
	os.Stdout = origStdout
	_, _ = buf.ReadFrom(r)

	if !dryRun {
		t.Error("expected dryRun flag to be true after --dry-run")
	}
	output := buf.String()
	if !strings.Contains(output, "starting sync operation") {
		t.Errorf("expected 'starting sync operation' in log output with --dry-run, got:\n%s", output)
	}
}

// TestCLI_Serve_RequiresServeEnabled verifies that the serve command returns an
// error when serve.enabled is not set in the config (the default).
func TestCLI_Serve_RequiresServeEnabled(t *testing.T) {
	origCfg := cfgFile
	t.Cleanup(func() { cfgFile = origCfg })

	cfgFile = writeTempConfig(t, t.TempDir())

	rootCmd.SetArgs([]string{"serve"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when serve.enabled is false, got nil")
	}
	if !strings.Contains(err.Error(), "serve") {
		t.Errorf("expected error to mention 'serve', got: %v", err)
	}
}

// TestCLI_LogLevelFlag verifies that --log-level debug causes setupLogger to
// produce a logger whose effective level is Debug.
func TestCLI_LogLevelFlag(t *testing.T) {
	origLevel := logLevel
	t.Cleanup(func() { logLevel = origLevel })

	logLevel = "debug"
	logger := setupLogger()

	if !logger.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("expected logger to be enabled at Debug level when --log-level debug")
	}
}
