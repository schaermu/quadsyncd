package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/schaermu/quadsyncd/internal/activation"
	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/git"
	"github.com/schaermu/quadsyncd/internal/logging"
	"github.com/schaermu/quadsyncd/internal/runstore"
	"github.com/schaermu/quadsyncd/internal/server"
	"github.com/schaermu/quadsyncd/internal/sync"
	"github.com/schaermu/quadsyncd/internal/systemduser"
	"github.com/spf13/cobra"
)

var (
	// Set by goreleaser
	version = "dev"
	commit  = "none"
	date    = "unknown"

	// Global flags
	cfgFile   string
	logLevel  string
	logFormat string
	dryRun    bool
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "quadsyncd",
	Short: "Synchronize Podman Quadlets from Git repositories",
	Long: `quadsyncd automatically synchronizes Podman Quadlet files from a Git repository
to a Podman-enabled server in rootless mode.

It can run as a oneshot sync (via systemd timer) or as a long-running webhook
daemon that responds to GitHub push events.`,
	SilenceUsage: true,
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Perform a one-time sync from repository to quadlet directory",
	Long: `Sync fetches the configured Git repository, compares quadlet files with the
local state, and applies changes to the systemd user quadlet directory.

After syncing files, it reloads the systemd daemon and optionally restarts
affected units based on the configured restart policy.`,
	RunE: runSync,
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the webhook server",
	Long: `Serve starts a long-running HTTP server that listens for GitHub webhook events
and triggers syncs when the configured repository is updated.

This mode requires additional configuration for webhook secrets and allowed refs.`,
	RunE: runServe,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("quadsyncd %s\n", version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", date)
	},
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.config/quadsyncd/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "text", "log format (text, json)")

	// Sync command flags
	syncCmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be done without making changes")

	// Add commands
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(versionCmd)
}

func runSync(cmd *cobra.Command, args []string) error {
	ctx, cancel := setupSignalHandler()
	defer cancel()

	// Setup console logger
	consoleLogger := setupLogger()

	// Load configuration
	cfg, err := loadConfig(consoleLogger)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize runstore
	store := runstore.NewStore(cfg.Paths.StateDir, consoleLogger)

	// Determine trigger source (default to CLI; timer should be detected via env)
	trigger := runstore.TriggerCLI
	if os.Getenv("INVOCATION_ID") != "" {
		// Running under systemd (timer or service)
		trigger = runstore.TriggerTimer
	}

	// Create initial run metadata
	meta := &runstore.RunMeta{
		Kind:      runstore.RunKindSync,
		Trigger:   trigger,
		StartedAt: time.Now().UTC(),
		Status:    runstore.RunStatusRunning,
		DryRun:    dryRun,
		Revisions: make(map[string]string),
		Conflicts: []runstore.ConflictSummary{},
	}

	if err := store.Create(ctx, meta); err != nil {
		consoleLogger.Error("failed to create run record", "error", err)
		return fmt.Errorf("failed to create run record: %w", err)
	}

	consoleLogger.Info("created run record", "run_id", meta.ID)

	// Parse log level for ndjson handler
	var ndjsonLevel slog.Level
	switch logLevel {
	case "debug":
		ndjsonLevel = slog.LevelDebug
	case "info":
		ndjsonLevel = slog.LevelInfo
	case "warn":
		ndjsonLevel = slog.LevelWarn
	case "error":
		ndjsonLevel = slog.LevelError
	default:
		ndjsonLevel = slog.LevelInfo
	}

	// Create a tee logger that writes to both console and runstore
	ndjsonHandler := logging.NewNDJSONHandler(func(line []byte) error {
		return store.AppendLog(ctx, meta.ID, line)
	}, &logging.NDJSONHandlerOptions{
		Level: ndjsonLevel,
	})

	teeHandler := logging.NewTeeHandler(consoleLogger.Handler(), ndjsonHandler)
	logger := slog.New(teeHandler)

	// Create dependencies
	factory := func(auth config.AuthConfig) git.Client {
		return git.NewShellClient(auth.SSHKeyFile, auth.HTTPSTokenFile)
	}
	systemdClient := systemduser.NewClient()

	// Create sync engine with tee logger
	engine := sync.NewEngineWithFactory(cfg, factory, systemdClient, logger, dryRun)

	// Run sync
	logger.Info("starting sync operation")
	result, syncErr := engine.Run(ctx)

	// Finalize run metadata
	endedAt := time.Now().UTC()
	meta.EndedAt = &endedAt

	if syncErr != nil {
		meta.Status = runstore.RunStatusError
		meta.Error = syncErr.Error()
		logger.Error("sync failed", "error", syncErr)
	} else {
		meta.Status = runstore.RunStatusSuccess
		logger.Info("sync completed successfully")
	}

	// Populate revisions and conflicts from result
	if result != nil {
		meta.Revisions = result.Revisions
		meta.Conflicts = make([]runstore.ConflictSummary, len(result.Conflicts))
		for i, c := range result.Conflicts {
			losers := make([]runstore.EffectiveItemSummary, len(c.Losers))
			for j, l := range c.Losers {
				losers[j] = runstore.EffectiveItemSummary{
					MergeKey:   c.MergeKey,
					SourceRepo: l.Repo,
					SourceRef:  l.Ref,
					SourceSHA:  l.SHA,
				}
			}
			meta.Conflicts[i] = runstore.ConflictSummary{
				MergeKey: c.MergeKey,
				Winner: runstore.EffectiveItemSummary{
					MergeKey:   c.MergeKey,
					SourceRepo: c.WinnerRepo,
					SourceRef:  c.WinnerRef,
					SourceSHA:  c.WinnerSHA,
				},
				Losers: losers,
			}
		}
	}

	// Update run metadata with final state
	if err := store.Update(ctx, meta); err != nil {
		logger.Error("failed to update run record", "error", err)
	}

	return syncErr
}

func runServe(cmd *cobra.Command, args []string) error {
	ctx, cancel := setupSignalHandler()
	defer cancel()

	// Setup logger
	logger := setupLogger()

	// Load configuration
	cfg, err := loadConfig(logger)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Validate serve configuration
	if !cfg.Serve.Enabled {
		return fmt.Errorf("serve mode is not enabled in config (set serve.enabled: true)")
	}

	// Initialize runstore
	store := runstore.NewStore(cfg.Paths.StateDir, logger)

	// Create dependencies
	gitFactory := func(auth config.AuthConfig) git.Client {
		return git.NewShellClient(auth.SSHKeyFile, auth.HTTPSTokenFile)
	}
	systemdClient := systemduser.NewClient()
	runnerFactory := sync.NewRunnerFactory(gitFactory, systemdClient)

	// Create webhook server
	server, err := server.NewServer(cfg, runnerFactory, systemdClient, store, logger)
	if err != nil {
		return fmt.Errorf("failed to create webhook server: %w", err)
	}

	// Check for systemd socket activation
	listeners, err := activation.Listeners()
	if err != nil {
		return fmt.Errorf("failed to check for socket activation: %w", err)
	}

	if len(listeners) > 0 {
		// Socket activation mode
		if len(listeners) > 1 {
			return fmt.Errorf("received %d socket-activated listeners, expected exactly 1", len(listeners))
		}

		listener := listeners[0]
		logger.Info("using systemd socket activation", "addr", listener.Addr().String(), "mode", "socket-activated")
		if err := server.StartWithListener(ctx, listener); err != nil {
			logger.Error("webhook server failed", "error", err)
			return err
		}
	} else {
		// Normal bind mode
		logger.Info("starting webhook server", "addr", cfg.Serve.ListenAddr, "mode", "bind")
		if err := server.Start(ctx); err != nil {
			logger.Error("webhook server failed", "error", err)
			return err
		}
	}

	logger.Info("webhook server stopped")
	return nil
}

func setupLogger() *slog.Logger {
	// Parse log level
	var level slog.Level
	switch logLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	// Create handler based on format
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level}

	if logFormat == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

func loadConfig(logger *slog.Logger) (*config.Config, error) {
	// Determine config file path
	configPath := cfgFile
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %w", err)
		}
		configPath = filepath.Join(home, ".config", "quadsyncd", "config.yaml")
	}

	logger.Info("loading configuration", "path", configPath)

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	logger.Debug("configuration loaded",
		"repositories", len(cfg.EffectiveRepositories()),
		"quadlet_dir", cfg.Paths.QuadletDir,
		"state_dir", cfg.Paths.StateDir)

	return cfg, nil
}

func setupSignalHandler() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		cancel()
	}()

	return ctx, cancel
}
