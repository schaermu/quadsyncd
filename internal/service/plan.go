package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/logging"
	"github.com/schaermu/quadsyncd/internal/quadlet"
	"github.com/schaermu/quadsyncd/internal/runstore"
	quadsyncd "github.com/schaermu/quadsyncd/internal/sync"
)

// PlanService orchestrates dry-run plan execution.
type PlanService struct {
	cfg           *config.Config
	runnerFactory quadsyncd.RunnerFactory
	store         runstore.ReadWriter
	logger        *slog.Logger
	secret        []byte
}

// NewPlanService creates a new PlanService.
func NewPlanService(cfg *config.Config, runnerFactory quadsyncd.RunnerFactory, store runstore.ReadWriter, logger *slog.Logger, secret []byte) *PlanService {
	return &PlanService{
		cfg:           cfg,
		runnerFactory: runnerFactory,
		store:         store,
		logger:        logger,
		secret:        secret,
	}
}

// Execute runs a dry-run plan for the given request and returns the run ID.
// If setup fails (run record cannot be created), returns ("", err).
// If the plan engine itself fails, returns (runID, err) – the run record is
// still created, updated, and accessible via the store.
func (p *PlanService) Execute(ctx context.Context, req runstore.PlanRequest) (string, error) {
	meta := &runstore.RunMeta{
		Kind:      runstore.RunKindPlan,
		Trigger:   runstore.TriggerUI,
		StartedAt: time.Now().UTC(),
		Status:    runstore.RunStatusRunning,
		DryRun:    true,
		Revisions: make(map[string]string),
		Conflicts: []runstore.ConflictSummary{},
	}

	if err := p.store.Create(ctx, meta); err != nil {
		p.logger.Error("failed to create plan run record", "error", err)
		return "", fmt.Errorf("failed to create plan record: %w", err)
	}
	p.logger.Info("created plan run record", "run_id", meta.ID)

	var ndjsonLevel = slog.LevelInfo
	if leveler, ok := p.logger.Handler().(interface{ Level() slog.Level }); ok {
		ndjsonLevel = leveler.Level()
	}

	ndjsonHandler := logging.NewNDJSONHandler(func(line []byte) error {
		return p.store.AppendLog(ctx, meta.ID, line)
	}, &logging.NDJSONHandlerOptions{
		Level: ndjsonLevel,
	})

	redactedNDJSON := logging.NewRedactingHandler(ndjsonHandler, []string{string(p.secret)})
	teeHandler := logging.NewTeeHandler(p.logger.Handler(), redactedNDJSON)
	logger := slog.New(teeHandler)

	workDir, err := p.store.WorkDirForRun(meta.ID)
	if err != nil {
		p.logger.Error("failed to resolve workdir for plan run", "run_id", meta.ID, "error", err)
		endedAt := time.Now().UTC()
		meta.EndedAt = &endedAt
		meta.Status = runstore.RunStatusError
		meta.Error = fmt.Sprintf("failed to resolve plan workdir: %v", err)
		if updateErr := p.store.Update(ctx, meta); updateErr != nil {
			p.logger.Error("failed to persist plan run error state after workdir failure", "run_id", meta.ID, "error", updateErr)
		}
		return meta.ID, fmt.Errorf("failed to resolve plan workdir: %w", err)
	}

	planOpts := quadsyncd.PlanEngineOptions{
		// Isolated checkout lives inside the run directory so it is automatically
		// cleaned up when the run is pruned from the store.
		WorkDir:    workDir,
		RepoFilter: req.RepoURL,
	}
	if req.RepoURL != "" && (req.Ref != "" || req.Commit != "") {
		planOpts.SpecOverrides = map[string]quadsyncd.SpecOverride{
			req.RepoURL: {
				Ref:    req.Ref,
				Commit: req.Commit,
			},
		}
	}

	logger.Info("performing plan operation",
		"repo_url", req.RepoURL,
		"ref", req.Ref,
		"commit", req.Commit)

	engine := p.runnerFactory(p.cfg, logger, true, &planOpts)
	result, planErr := engine.Run(ctx)

	endedAt := time.Now().UTC()
	meta.EndedAt = &endedAt

	if planErr != nil {
		meta.Status = runstore.RunStatusError
		meta.Error = planErr.Error()
		logger.Error("plan failed", "error", planErr)
	} else {
		meta.Status = runstore.RunStatusSuccess
		logger.Info("plan completed successfully")
	}

	if result != nil {
		meta.Revisions = result.Revisions
		meta.Conflicts = make([]runstore.ConflictSummary, len(result.Conflicts))
		for i, c := range result.Conflicts {
			meta.Conflicts[i] = conflictSummaryFromSync(c)
		}
	}

	if result != nil && result.Plan != nil {
		planData := writePlanWithArtifacts(ctx, p.store, meta.ID, result.Plan, meta.Conflicts, p.cfg.Paths.QuadletDir, req, logger)
		if err := p.store.WritePlan(ctx, meta.ID, planData); err != nil {
			logger.Error("failed to persist plan.json", "error", err)
		}
	}

	if err := p.store.Update(ctx, meta); err != nil {
		logger.Error("failed to update plan run record", "error", err)
	}

	return meta.ID, planErr
}

// writePlanWithArtifacts converts a sync.Plan to runstore.Plan, persists before/after
// artifacts for quadlet-only files, and returns the populated Plan ready for storage.
// Non-quadlet companion files are never read or stored.
// PlanOp.Path is stored relative to quadletDir for API stability.
func writePlanWithArtifacts(ctx context.Context, store runstore.ReadWriter, runID string, syncPlan *quadsyncd.Plan, conflicts []runstore.ConflictSummary, quadletDir string, requested runstore.PlanRequest, logger *slog.Logger) runstore.Plan {
	ops := make([]runstore.PlanOp, 0, len(syncPlan.Add)+len(syncPlan.Update)+len(syncPlan.Delete))
	idx := 0

	relPath := func(abs string) string {
		rel, err := filepath.Rel(quadletDir, abs)
		if err != nil {
			return abs // fallback: keep abs path rather than silently dropping
		}
		return filepath.ToSlash(rel)
	}

	writeArtifact := func(name string, path string) string {
		content, err := os.ReadFile(path)
		if err != nil {
			logger.Warn("plan artifact: failed to read file", "path", path, "error", err)
			return ""
		}
		if err := store.WriteArtifact(ctx, runID, name, content); err != nil {
			logger.Warn("plan artifact: failed to write artifact", "name", name, "error", err)
			return ""
		}
		return name
	}

	for _, op := range syncPlan.Add {
		pOp := runstore.PlanOp{
			Op:         "add",
			Path:       relPath(op.DestPath),
			SourceRepo: op.SourceRepo,
			SourceRef:  op.SourceRef,
			SourceSHA:  op.SourceSHA,
		}
		if quadlet.IsQuadletFile(op.DestPath) {
			pOp.Unit = quadlet.UnitNameFromQuadlet(op.DestPath)
			ext := filepath.Ext(op.DestPath)
			afterName := fmt.Sprintf("%04d-after%s", idx, ext)
			pOp.AfterPath = writeArtifact(afterName, op.SourcePath)
		}
		ops = append(ops, pOp)
		idx++
	}

	for _, op := range syncPlan.Update {
		pOp := runstore.PlanOp{
			Op:         "update",
			Path:       relPath(op.DestPath),
			SourceRepo: op.SourceRepo,
			SourceRef:  op.SourceRef,
			SourceSHA:  op.SourceSHA,
		}
		if quadlet.IsQuadletFile(op.DestPath) {
			pOp.Unit = quadlet.UnitNameFromQuadlet(op.DestPath)
			ext := filepath.Ext(op.DestPath)
			beforeName := fmt.Sprintf("%04d-before%s", idx, ext)
			afterName := fmt.Sprintf("%04d-after%s", idx, ext)
			// "before": current file on disk in quadletDir
			pOp.BeforePath = writeArtifact(beforeName, op.DestPath)
			// "after": incoming content from source checkout
			pOp.AfterPath = writeArtifact(afterName, op.SourcePath)
		}
		ops = append(ops, pOp)
		idx++
	}

	for _, op := range syncPlan.Delete {
		pOp := runstore.PlanOp{
			Op:   "delete",
			Path: relPath(op.DestPath),
		}
		if quadlet.IsQuadletFile(op.DestPath) {
			pOp.Unit = quadlet.UnitNameFromQuadlet(op.DestPath)
			ext := filepath.Ext(op.DestPath)
			beforeName := fmt.Sprintf("%04d-before%s", idx, ext)
			// "before": current file on disk (what will be removed)
			pOp.BeforePath = writeArtifact(beforeName, op.DestPath)
		}
		ops = append(ops, pOp)
		idx++
	}

	return runstore.Plan{
		Requested: requested,
		Conflicts: conflicts,
		Ops:       ops,
	}
}
