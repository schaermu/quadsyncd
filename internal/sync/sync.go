package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/git"
	"github.com/schaermu/quadsyncd/internal/quadlet"
	"github.com/schaermu/quadsyncd/internal/systemduser"
)

// Engine orchestrates the sync process
type Engine struct {
	cfg     *config.Config
	git     git.Client
	systemd systemduser.Systemd
	logger  *slog.Logger
	dryRun  bool
}

// NewEngine creates a new sync engine
func NewEngine(cfg *config.Config, gitClient git.Client, systemd systemduser.Systemd, logger *slog.Logger, dryRun bool) *Engine {
	return &Engine{
		cfg:     cfg,
		git:     gitClient,
		systemd: systemd,
		logger:  logger,
		dryRun:  dryRun,
	}
}

// Run executes the complete sync process
func (e *Engine) Run(ctx context.Context) error {
	e.logger.Info("starting sync",
		"repo", e.cfg.Repo.URL,
		"ref", e.cfg.Repo.Ref,
		"dry_run", e.dryRun)

	// Ensure state directory exists
	if err := os.MkdirAll(e.cfg.Paths.StateDir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// Fetch repository
	e.logger.Info("fetching repository", "dest", e.cfg.RepoDir())
	commit, err := e.git.EnsureCheckout(ctx, e.cfg.Repo.URL, e.cfg.Repo.Ref, e.cfg.RepoDir())
	if err != nil {
		return fmt.Errorf("failed to checkout repository: %w", err)
	}
	e.logger.Info("repository checked out", "commit", commit)

	// Load previous state
	prevState, err := e.loadState()
	if err != nil {
		e.logger.Warn("failed to load previous state (will treat as fresh sync)", "error", err)
		prevState = &State{ManagedFiles: make(map[string]ManagedFile)}
	}

	// Build current desired state
	plan, err := e.buildPlan(prevState)
	if err != nil {
		return fmt.Errorf("failed to build sync plan: %w", err)
	}

	// Log plan
	e.logger.Info("sync plan",
		"add", len(plan.Add),
		"update", len(plan.Update),
		"delete", len(plan.Delete))

	// check for dry-run mode
	if e.dryRun {
		e.logPlanDetails(plan)
		e.logger.Info("dry-run complete, no changes applied")
		return nil
	}

	// Check systemd availability
	available, err := e.systemd.IsAvailable(ctx)
	if err != nil || !available {
		return fmt.Errorf("systemd user session not available: %w", err)
	}

	// Apply plan
	if err := e.applyPlan(plan); err != nil {
		return fmt.Errorf("failed to apply sync plan: %w", err)
	}

	// Validate quadlet definitions
	e.logger.Info("validating quadlet definitions", "quadlet_dir", e.cfg.Paths.QuadletDir)
	if err := e.systemd.ValidateQuadlets(ctx, e.cfg.Paths.QuadletDir); err != nil {
		return fmt.Errorf("failed to validate quadlet definitions: %w", err)
	}

	// Save new state
	newState := e.buildState(prevState, plan, commit)
	if err := e.saveState(newState); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}

	// Reload systemd
	e.logger.Info("reloading systemd daemon")
	if err := e.systemd.DaemonReload(ctx); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}

	// Handle restarts based on policy
	if err := e.handleRestarts(ctx, plan, newState); err != nil {
		e.logger.Warn("restart operations had issues", "error", err)
		// Don't fail the entire sync for restart issues
	}

	e.logger.Info("sync completed successfully")
	return nil
}

// buildPlan computes the diff between desired and current state
func (e *Engine) buildPlan(prevState *State) (*Plan, error) {
	plan := &Plan{
		Add:    make([]FileOp, 0),
		Update: make([]FileOp, 0),
		Delete: make([]FileOp, 0),
	}

	// Discover all files in the source directory (quadlet files and companions)
	sourceFiles, err := quadlet.DiscoverAllFiles(e.cfg.QuadletSourceDir())
	if err != nil {
		return nil, fmt.Errorf("failed to discover source files: %w", err)
	}

	e.logger.Info("discovered source files", "count", len(sourceFiles))

	// Build map of current desired files
	desiredFiles := make(map[string]string) // destPath -> sourcePath
	for _, srcPath := range sourceFiles {
		relPath, err := quadlet.RelativePath(e.cfg.QuadletSourceDir(), srcPath)
		if err != nil {
			return nil, fmt.Errorf("failed to compute relative path: %w", err)
		}

		destPath := filepath.Join(e.cfg.Paths.QuadletDir, relPath)
		desiredFiles[destPath] = srcPath
	}

	// Compute operations
	for destPath, srcPath := range desiredFiles {
		hash, err := fileHash(srcPath)
		if err != nil {
			return nil, fmt.Errorf("failed to compute hash for %s: %w", srcPath, err)
		}

		prev, exists := prevState.ManagedFiles[destPath]
		if !exists {
			// New file
			plan.Add = append(plan.Add, FileOp{
				SourcePath: srcPath,
				DestPath:   destPath,
				Hash:       hash,
			})
		} else if prev.Hash != hash {
			// File changed
			plan.Update = append(plan.Update, FileOp{
				SourcePath: srcPath,
				DestPath:   destPath,
				Hash:       hash,
			})
		}
		// else: unchanged, no action needed
	}

	// Find files to delete (if prune is enabled)
	if e.cfg.Sync.Prune {
		for destPath := range prevState.ManagedFiles {
			if _, exists := desiredFiles[destPath]; !exists {
				plan.Delete = append(plan.Delete, FileOp{
					DestPath: destPath,
				})
			}
		}
	}

	return plan, nil
}

// applyPlan executes the sync plan
func (e *Engine) applyPlan(plan *Plan) error {
	// Ensure destination directory exists
	if err := os.MkdirAll(e.cfg.Paths.QuadletDir, 0755); err != nil {
		return fmt.Errorf("failed to create quadlet directory: %w", err)
	}

	// Add new files
	for _, op := range plan.Add {
		e.logger.Info("adding file", "dest", op.DestPath)
		if err := e.copyFile(op.SourcePath, op.DestPath); err != nil {
			return fmt.Errorf("failed to add file %s: %w", op.DestPath, err)
		}
	}

	// Update existing files
	for _, op := range plan.Update {
		e.logger.Info("updating file", "dest", op.DestPath)
		if err := e.copyFile(op.SourcePath, op.DestPath); err != nil {
			return fmt.Errorf("failed to update file %s: %w", op.DestPath, err)
		}
	}

	// Delete removed files
	for _, op := range plan.Delete {
		e.logger.Info("deleting file", "dest", op.DestPath)
		if err := os.Remove(op.DestPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to delete file %s: %w", op.DestPath, err)
		}
	}

	return nil
}

// copyFile copies a file from src to dst with atomic write
func (e *Engine) copyFile(src, dst string) error {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	// Open source
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		_ = srcFile.Close()
	}()

	// Create temp file in destination directory
	tmpFile, err := os.CreateTemp(filepath.Dir(dst), ".quadsyncd-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}() // cleanup on error

	// Copy content
	if _, err := io.Copy(tmpFile, srcFile); err != nil {
		_ = tmpFile.Close()
		return err
	}

	// Get source permissions
	srcInfo, err := srcFile.Stat()
	if err != nil {
		_ = tmpFile.Close()
		return err
	}

	// Set permissions on temp file
	if err := tmpFile.Chmod(srcInfo.Mode()); err != nil {
		_ = tmpFile.Close()
		return err
	}

	// Close temp file
	if err := tmpFile.Close(); err != nil {
		return err
	}

	// Atomic rename
	if err := os.Rename(tmpPath, dst); err != nil {
		return err
	}

	return nil
}

// handleRestarts restarts units based on the configured policy
func (e *Engine) handleRestarts(ctx context.Context, plan *Plan, state *State) error {
	switch e.cfg.Sync.Restart {
	case config.RestartNone:
		e.logger.Info("restart policy: none, skipping restarts")
		return nil

	case config.RestartChanged:
		// Restart only units affected by changes
		units := e.affectedUnits(plan)
		if len(units) == 0 {
			e.logger.Info("no units affected by changes")
			return nil
		}
		e.logger.Info("restarting affected units", "count", len(units), "units", units)
		return e.systemd.TryRestartUnits(ctx, units)

	case config.RestartAllManaged:
		// Restart all managed units
		units := e.allManagedUnits(state)
		if len(units) == 0 {
			e.logger.Info("no managed units to restart")
			return nil
		}
		e.logger.Info("restarting all managed units", "count", len(units))
		return e.systemd.TryRestartUnits(ctx, units)

	default:
		return fmt.Errorf("unknown restart policy: %s", e.cfg.Sync.Restart)
	}
}

// affectedUnits returns unit names affected by the plan (added, updated, or deleted).
func (e *Engine) affectedUnits(plan *Plan) []string {
	ops := make([]FileOp, 0, len(plan.Add)+len(plan.Update)+len(plan.Delete))
	ops = append(ops, plan.Add...)
	ops = append(ops, plan.Update...)
	ops = append(ops, plan.Delete...)
	return quadletUnitsFromOps(ops)
}

// allManagedUnits returns every unit tracked in state (not just changed ones).
func (e *Engine) allManagedUnits(state *State) []string {
	units := make(map[string]bool)
	for destPath := range state.ManagedFiles {
		if quadlet.IsQuadletFile(destPath) {
			units[quadlet.UnitNameFromQuadlet(destPath)] = true
		}
	}

	result := make([]string, 0, len(units))
	for unit := range units {
		result = append(result, unit)
	}
	return result
}

// quadletUnitsFromOps extracts unique systemd unit names from file operations,
// considering only quadlet files (companion files do not generate units).
func quadletUnitsFromOps(ops []FileOp) []string {
	units := make(map[string]bool)
	for _, op := range ops {
		if quadlet.IsQuadletFile(op.DestPath) {
			units[quadlet.UnitNameFromQuadlet(op.DestPath)] = true
		}
	}

	result := make([]string, 0, len(units))
	for unit := range units {
		result = append(result, unit)
	}
	return result
}

// logPlanDetails logs detailed plan information for dry-run
func (e *Engine) logPlanDetails(plan *Plan) {
	for _, op := range plan.Add {
		e.logger.Info("[dry-run] would add", "dest", op.DestPath, "source", op.SourcePath)
	}
	for _, op := range plan.Update {
		e.logger.Info("[dry-run] would update", "dest", op.DestPath, "source", op.SourcePath)
	}
	for _, op := range plan.Delete {
		e.logger.Info("[dry-run] would delete", "dest", op.DestPath)
	}
}

// buildState creates a new State from the applied plan
func (e *Engine) buildState(prevState *State, plan *Plan, commit string) *State {
	state := &State{
		Commit:       commit,
		ManagedFiles: make(map[string]ManagedFile),
	}

	if prevState != nil {
		for destPath, managed := range prevState.ManagedFiles {
			state.ManagedFiles[destPath] = managed
		}
	}

	for _, op := range plan.Delete {
		delete(state.ManagedFiles, op.DestPath)
	}

	for _, op := range append(plan.Add, plan.Update...) {
		relPath, _ := quadlet.RelativePath(e.cfg.QuadletSourceDir(), op.SourcePath)
		state.ManagedFiles[op.DestPath] = ManagedFile{
			SourcePath: relPath,
			Hash:       op.Hash,
		}
	}

	return state
}

// loadState loads the previous state from disk
func (e *Engine) loadState() (*State, error) {
	data, err := os.ReadFile(e.cfg.StateFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return &State{ManagedFiles: make(map[string]ManagedFile)}, nil
		}
		return nil, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	return &state, nil
}

// saveState persists the state to disk
func (e *Engine) saveState(state *State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(e.cfg.StateFilePath(), data, 0644)
}

// fileHash computes the SHA256 hash of a file
func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
	}()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
