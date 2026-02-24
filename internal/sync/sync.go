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
	"sort"
	"strings"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/git"
	"github.com/schaermu/quadsyncd/internal/multirepo"
	"github.com/schaermu/quadsyncd/internal/quadlet"
	"github.com/schaermu/quadsyncd/internal/systemduser"
)

// GitClientFactory creates a git.Client for a given AuthConfig.
// Used to produce per-repo clients when auth overrides are configured.
type GitClientFactory func(auth config.AuthConfig) git.Client

// Engine orchestrates the sync process
type Engine struct {
	cfg        *config.Config
	git        git.Client
	gitFactory GitClientFactory
	systemd    systemduser.Systemd
	logger     *slog.Logger
	dryRun     bool
}

// NewEngine creates a new sync engine using a single git client for all repos.
func NewEngine(cfg *config.Config, gitClient git.Client, systemd systemduser.Systemd, logger *slog.Logger, dryRun bool) *Engine {
	return &Engine{
		cfg:     cfg,
		git:     gitClient,
		systemd: systemd,
		logger:  logger,
		dryRun:  dryRun,
	}
}

// NewEngineWithFactory creates a sync engine that uses a factory to produce
// per-repo git clients (supports per-repo auth overrides in multi-repo mode).
func NewEngineWithFactory(cfg *config.Config, factory GitClientFactory, systemd systemduser.Systemd, logger *slog.Logger, dryRun bool) *Engine {
	return &Engine{
		cfg:        cfg,
		gitFactory: factory,
		systemd:    systemd,
		logger:     logger,
		dryRun:     dryRun,
	}
}

// Run executes the complete sync process
func (e *Engine) Run(ctx context.Context) error {
	repos := e.cfg.EffectiveRepositories()

	e.logger.Info("starting sync",
		"repo_count", len(repos),
		"dry_run", e.dryRun)

	// Ensure state directory exists
	if err := os.MkdirAll(e.cfg.Paths.StateDir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// Load all repo states (fail-fast: if any repo fails, nothing is applied)
	repoStates, err := e.loadAllRepoStates(ctx, repos)
	if err != nil {
		return err
	}

	for _, rs := range repoStates {
		e.logger.Info("repository loaded",
			"repo", rs.Spec.URL,
			"ref", rs.Spec.Ref,
			"commit", rs.Commit,
			"files", len(rs.Files))
	}

	// Merge repo states into effective state
	conflictMode := e.cfg.Sync.ConflictHandling
	if conflictMode == "" {
		conflictMode = config.ConflictPreferHighestPriority
	}
	mergeResult, err := multirepo.Merge(repoStates, conflictMode)
	if err != nil {
		return fmt.Errorf("failed to merge repository states: %w", err)
	}

	// Warn on same-path conflicts in prefer mode
	for _, c := range mergeResult.Conflicts {
		loserRepos := make([]string, len(c.Losers))
		for i, l := range c.Losers {
			loserRepos[i] = fmt.Sprintf("%s@%s", l.SourceRepo, l.SourceRef)
		}
		e.logger.Warn("same-path conflict resolved by priority",
			"path", c.MergeKey,
			"winner_repo", c.Winner.SourceRepo,
			"winner_ref", c.Winner.SourceRef,
			"losers", strings.Join(loserRepos, ", "),
			"remediation", "adjust priorities or remove duplicate definitions")
	}

	e.logger.Info("merge complete",
		"total_files", len(mergeResult.Items),
		"conflicts", len(mergeResult.Conflicts))

	// Load previous state
	prevState, err := e.loadState()
	if err != nil {
		e.logger.Warn("failed to load previous state (will treat as fresh sync)", "error", err)
		prevState = &State{ManagedFiles: make(map[string]ManagedFile)}
	}

	// Build sync plan from effective items
	plan, err := e.buildPlanFromEffective(prevState, mergeResult.Items)
	if err != nil {
		return fmt.Errorf("failed to build sync plan: %w", err)
	}

	e.logger.Info("sync plan",
		"add", len(plan.Add),
		"update", len(plan.Update),
		"delete", len(plan.Delete))

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
	newState := e.buildStateFromEffective(prevState, plan, repoStates)
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
	}

	e.logger.Info("sync completed successfully")
	return nil
}

// loadAllRepoStates loads all repositories fail-fast.
// If any repo fails to load, the function returns immediately.
func (e *Engine) loadAllRepoStates(ctx context.Context, repos []config.RepoSpec) ([]multirepo.RepoState, error) {
	states := make([]multirepo.RepoState, 0, len(repos))

	for _, spec := range repos {
		var gitClient git.Client
		if e.gitFactory != nil {
			gitClient = e.gitFactory(e.cfg.AuthForSpec(spec))
		} else {
			gitClient = e.git
		}

		repoDir := e.cfg.RepoDirForSpec(spec)
		srcDir := e.cfg.QuadletSourceDirForSpec(spec)

		e.logger.Info("fetching repository", "repo", spec.URL, "ref", spec.Ref, "dest", repoDir)

		rs, err := multirepo.LoadRepoState(ctx, spec, repoDir, srcDir, gitClient)
		if err != nil {
			return nil, err
		}
		states = append(states, rs)
	}

	return states, nil
}

// buildPlanFromEffective computes the diff between the effective items (from
// multi-repo merge) and the previously managed state.
func (e *Engine) buildPlanFromEffective(prevState *State, items []multirepo.EffectiveItem) (*Plan, error) {
	plan := &Plan{
		Add:    make([]FileOp, 0),
		Update: make([]FileOp, 0),
		Delete: make([]FileOp, 0),
	}

	// Build map of desired dest paths
	desiredFiles := make(map[string]multirepo.EffectiveItem)
	for _, item := range items {
		destPath := filepath.Join(e.cfg.Paths.QuadletDir, filepath.FromSlash(item.MergeKey))
		desiredFiles[destPath] = item
	}

	// Compute add / update
	for destPath, item := range desiredFiles {
		hash, err := fileHash(item.AbsPath)
		if err != nil {
			return nil, fmt.Errorf("failed to compute hash for %s: %w", item.AbsPath, err)
		}

		prev, exists := prevState.ManagedFiles[destPath]
		op := FileOp{
			SourcePath: item.AbsPath,
			DestPath:   destPath,
			Hash:       hash,
			SourceRepo: item.SourceRepo,
			SourceRef:  item.SourceRef,
			SourceSHA:  item.SourceSHA,
		}
		if !exists {
			plan.Add = append(plan.Add, op)
		} else if prev.Hash != hash {
			plan.Update = append(plan.Update, op)
		}
	}

	// Compute deletes (if prune enabled)
	if e.cfg.Sync.Prune {
		for destPath := range prevState.ManagedFiles {
			if _, exists := desiredFiles[destPath]; !exists {
				plan.Delete = append(plan.Delete, FileOp{DestPath: destPath})
			}
		}
	}

	// Sort for deterministic output
	sort.Slice(plan.Add, func(i, j int) bool { return plan.Add[i].DestPath < plan.Add[j].DestPath })
	sort.Slice(plan.Update, func(i, j int) bool { return plan.Update[i].DestPath < plan.Update[j].DestPath })
	sort.Slice(plan.Delete, func(i, j int) bool { return plan.Delete[i].DestPath < plan.Delete[j].DestPath })

	return plan, nil
}

// applyPlan executes the sync plan
func (e *Engine) applyPlan(plan *Plan) error {
	if err := os.MkdirAll(e.cfg.Paths.QuadletDir, 0755); err != nil {
		return fmt.Errorf("failed to create quadlet directory: %w", err)
	}

	for _, op := range plan.Add {
		e.logger.Info("adding file", "dest", op.DestPath)
		if err := e.copyFile(op.SourcePath, op.DestPath); err != nil {
			return fmt.Errorf("failed to add file %s: %w", op.DestPath, err)
		}
	}

	for _, op := range plan.Update {
		e.logger.Info("updating file", "dest", op.DestPath)
		if err := e.copyFile(op.SourcePath, op.DestPath); err != nil {
			return fmt.Errorf("failed to update file %s: %w", op.DestPath, err)
		}
	}

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
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		_ = srcFile.Close()
	}()

	tmpFile, err := os.CreateTemp(filepath.Dir(dst), ".quadsyncd-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := io.Copy(tmpFile, srcFile); err != nil {
		_ = tmpFile.Close()
		return err
	}

	srcInfo, err := srcFile.Stat()
	if err != nil {
		_ = tmpFile.Close()
		return err
	}

	if err := tmpFile.Chmod(srcInfo.Mode()); err != nil {
		_ = tmpFile.Close()
		return err
	}

	if err := tmpFile.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, dst)
}

// handleRestarts restarts units based on the configured policy
func (e *Engine) handleRestarts(ctx context.Context, plan *Plan, state *State) error {
	switch e.cfg.Sync.Restart {
	case config.RestartNone:
		e.logger.Info("restart policy: none, skipping restarts")
		return nil

	case config.RestartChanged:
		units := e.affectedUnits(plan)
		if len(units) == 0 {
			e.logger.Info("no units affected by changes")
			return nil
		}
		e.logger.Info("restarting affected units", "count", len(units), "units", units)
		return e.systemd.TryRestartUnits(ctx, units)

	case config.RestartAllManaged:
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

// quadletUnitsFromOps extracts unique systemd unit names from file operations.
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

// buildStateFromEffective creates a new State from the applied plan with provenance.
func (e *Engine) buildStateFromEffective(prevState *State, plan *Plan, repoStates []multirepo.RepoState) *State {
	state := &State{
		Revisions:    make(map[string]string),
		ManagedFiles: make(map[string]ManagedFile),
	}

	for _, rs := range repoStates {
		state.Revisions[rs.Spec.URL] = rs.Commit
	}
	// For single-repo backward compat, also set the top-level Commit field.
	if len(repoStates) == 1 {
		state.Commit = repoStates[0].Commit
	}

	if prevState != nil {
		for k, v := range prevState.ManagedFiles {
			state.ManagedFiles[k] = v
		}
	}

	for _, op := range plan.Delete {
		delete(state.ManagedFiles, op.DestPath)
	}

	for _, op := range append(plan.Add, plan.Update...) {
		relPath, err := filepath.Rel(e.cfg.Paths.QuadletDir, op.DestPath)
		if err != nil {
			e.logger.Error("failed to compute relative path for managed file",
				"quadletDir", e.cfg.Paths.QuadletDir,
				"destPath", op.DestPath,
				"error", err,
			)
			relPath = op.DestPath
		}
		state.ManagedFiles[op.DestPath] = ManagedFile{
			SourcePath: filepath.ToSlash(relPath),
			Hash:       op.Hash,
			SourceRepo: op.SourceRepo,
			SourceRef:  op.SourceRef,
			SourceSHA:  op.SourceSHA,
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
