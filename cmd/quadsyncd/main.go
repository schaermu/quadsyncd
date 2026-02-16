package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/git"
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
	Short: "Start the webhook server (planned for future)",
	Long: `Serve starts a long-running HTTP server that listens for GitHub webhook events
and triggers syncs when the configured repository is updated.

This mode requires additional configuration for webhook secrets and allowed refs.
Note: This command is planned for a future release.`,
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

	// Setup logger
	logger := setupLogger()

	// Load configuration
	cfg, err := loadConfig(logger)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Create dependencies
	gitClient := git.NewShellClient(cfg.Auth.SSHKeyFile, cfg.Auth.HTTPSTokenFile)
	systemdClient := systemduser.NewClient()

	// Create sync engine
	engine := sync.NewEngine(cfg, gitClient, systemdClient, logger, dryRun)

	// Run sync
	logger.Info("starting sync operation")
	if err := engine.Run(ctx); err != nil {
		logger.Error("sync failed", "error", err)
		return err
	}

	return nil
}

func runServe(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("serve command not yet implemented (planned for future release)")
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
		configPath = fmt.Sprintf("%s/.config/quadsyncd/config.yaml", home)
	}

	logger.Info("loading configuration", "path", configPath)

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	logger.Debug("configuration loaded",
		"repo", cfg.Repo.URL,
		"ref", cfg.Repo.Ref,
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
