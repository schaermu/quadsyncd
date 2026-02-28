// Package service implements the business logic layer for sync and plan operations.
package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/logging"
	"github.com/schaermu/quadsyncd/internal/runstore"
	quadsyncd "github.com/schaermu/quadsyncd/internal/sync"
)

// SyncService orchestrates sync execution with run tracking and single-flight semantics.
type SyncService struct {
	cfg           *config.Config
	runnerFactory quadsyncd.RunnerFactory
	store         runstore.ReadWriter
	logger        *slog.Logger
	secret        []byte

	mu      sync.Mutex // guards running and pending
	running bool       // whether a sync is currently in progress
	pending bool       // whether another sync is needed after the current one
}

// NewSyncService creates a new SyncService.
func NewSyncService(cfg *config.Config, runnerFactory quadsyncd.RunnerFactory, store runstore.ReadWriter, logger *slog.Logger, secret []byte) *SyncService {
	return &SyncService{
		cfg:           cfg,
		runnerFactory: runnerFactory,
		store:         store,
		logger:        logger,
		secret:        secret,
	}
}

// TriggerSync enqueues a sync. Uses single-flight semantics:
//   - If no sync is running: starts one immediately in the caller's goroutine.
//   - If a sync is already running: marks pending and returns; the running sync
//     loop will service the queued request automatically.
//   - At most one additional run is ever queued; further concurrent calls drop.
func (s *SyncService) TriggerSync(ctx context.Context, trigger runstore.TriggerSource) {
	s.mu.Lock()
	if s.running {
		s.pending = true
		s.mu.Unlock()
		s.logger.Info("sync already in progress, queuing pending re-run")
		return
	}
	s.running = true
	s.mu.Unlock()

	runCtx := ctx
	for {
		s.executeSync(runCtx, trigger)

		// Atomically check whether another sync was requested while we were
		// running. If not, release the running slot and stop; if yes, clear
		// the flag and loop to service that one pending request.
		s.mu.Lock()
		if !s.pending {
			s.running = false
			s.mu.Unlock()
			break
		}
		s.pending = false
		s.mu.Unlock()

		// The pending request arrived independently of the original caller;
		// use a fresh background context so that cancellation of the initial
		// context (e.g. server shutdown signalled after the first sync was
		// already queued) does not abort the re-run.
		runCtx = context.Background()
		s.logger.Info("re-running sync due to pending request")
	}
}

// executeSync performs a single instrumented sync run: creates a run record,
// sets up tee logging, runs the engine, and persists results.
func (s *SyncService) executeSync(ctx context.Context, trigger runstore.TriggerSource) {
	meta := &runstore.RunMeta{
		Kind:      runstore.RunKindSync,
		Trigger:   trigger,
		StartedAt: time.Now().UTC(),
		Status:    runstore.RunStatusRunning,
		DryRun:    false,
		Revisions: make(map[string]string),
		Conflicts: []runstore.ConflictSummary{},
	}

	runRecordCreated := false
	if err := s.store.Create(ctx, meta); err != nil {
		s.logger.Error("failed to create run record, continuing without instrumentation", "error", err)
		// Run sync without runstore instrumentation as a best-effort fallback.
		engine := s.runnerFactory(s.cfg, s.logger, false, nil)
		_, syncErr := engine.Run(ctx)
		if syncErr != nil {
			s.logger.Error("sync failed", "error", syncErr)
		} else {
			s.logger.Info("sync completed successfully")
		}
		return
	}
	runRecordCreated = true
	s.logger.Info("created run record", "run_id", meta.ID)

	// Determine the log level to use for the ndjson file handler.
	var ndjsonLevel = slog.LevelInfo
	if leveler, ok := s.logger.Handler().(interface{ Level() slog.Level }); ok {
		ndjsonLevel = leveler.Level()
	}

	ndjsonHandler := logging.NewNDJSONHandler(func(line []byte) error {
		return s.store.AppendLog(ctx, meta.ID, line)
	}, &logging.NDJSONHandlerOptions{
		Level: ndjsonLevel,
	})

	// Wrap the ndjson handler with secret redaction so known sensitive values
	// (e.g. the webhook secret) are not written to stored run logs.
	redactedNDJSON := logging.NewRedactingHandler(ndjsonHandler, []string{string(s.secret)})

	teeHandler := logging.NewTeeHandler(s.logger.Handler(), redactedNDJSON)
	logger := slog.New(teeHandler)

	logger.Info("performing sync operation")
	engine := s.runnerFactory(s.cfg, logger, false, nil)
	result, syncErr := engine.Run(ctx)

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

	if result != nil {
		meta.Revisions = result.Revisions
		meta.Conflicts = make([]runstore.ConflictSummary, len(result.Conflicts))
		for i, c := range result.Conflicts {
			meta.Conflicts[i] = conflictSummaryFromSync(c)
		}
	}

	if runRecordCreated {
		if err := s.store.Update(ctx, meta); err != nil {
			logger.Error("failed to update run record", "error", err)
		}
	}
}
