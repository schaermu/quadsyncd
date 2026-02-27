package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/git"
	"github.com/schaermu/quadsyncd/internal/logging"
	"github.com/schaermu/quadsyncd/internal/quadlet"
	"github.com/schaermu/quadsyncd/internal/runstore"
	quadsyncd "github.com/schaermu/quadsyncd/internal/sync"
	"github.com/schaermu/quadsyncd/internal/systemduser"
)

// GitHubPushEvent represents the relevant fields from a GitHub push webhook
type GitHubPushEvent struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Repository struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
		SSHURL   string `json:"ssh_url"`
	} `json:"repository"`
}

// GitClientFactory creates a git.Client for a given AuthConfig.
type GitClientFactory func(auth config.AuthConfig) git.Client

// Server implements the webhook HTTP server
type Server struct {
	cfg         *config.Config
	gitFactory  GitClientFactory
	systemd     systemduser.Systemd
	logger      *slog.Logger
	store       *runstore.Store
	broadcaster *Broadcaster
	secret      []byte
	syncMu      sync.Mutex // guards syncRunning and syncPending
	syncRunning bool       // whether a sync is currently in progress
	syncPending bool       // whether another sync is needed after the current one
	debounce    *debouncer
}

// debouncer implements debouncing for webhook events
type debouncer struct {
	mu       sync.Mutex
	timer    *time.Timer
	delay    time.Duration
	callback func()
}

// NewServer creates a new webhook server
func NewServer(cfg *config.Config, gitFactory GitClientFactory, systemd systemduser.Systemd, store *runstore.Store, logger *slog.Logger) (*Server, error) {
	// Validate required parameters
	if store == nil {
		return nil, fmt.Errorf("runstore.Store cannot be nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}
	if systemd == nil {
		return nil, fmt.Errorf("systemd client cannot be nil")
	}

	// Load webhook secret from file
	secret, err := os.ReadFile(cfg.Serve.GitHubWebhookSecretFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read webhook secret: %w", err)
	}

	// Trim any whitespace/newlines from secret
	secret = []byte(strings.TrimSpace(string(secret)))

	s := &Server{
		cfg:        cfg,
		gitFactory: gitFactory,
		systemd:    systemd,
		logger:     logger,
		store:      store,
		secret:     secret,
	}

	// Initialize debouncer with 2 second delay
	s.debounce = &debouncer{
		delay: 2 * time.Second,
	}

	// Initialize SSE broadcaster watching the runs directory.
	runsDir := filepath.Join(cfg.Paths.StateDir, "runs")
	s.broadcaster = newBroadcaster(runsDir, logger, defaultBroadcastInterval)

	return s, nil
}

// Start starts the webhook HTTP server, performing an initial sync first.
// It binds to the configured address.
func (s *Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.cfg.Serve.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to bind to %s: %w", s.cfg.Serve.ListenAddr, err)
	}

	s.logger.Info("webhook server bound to address", "addr", s.cfg.Serve.ListenAddr, "mode", "bind")
	return s.StartWithListener(ctx, listener)
}

// StartWithListener starts the webhook HTTP server using a provided listener,
// performing an initial sync first. This supports systemd socket activation.
func (s *Server) StartWithListener(ctx context.Context, listener net.Listener) error {
	s.logger.Info("performing initial sync before starting webhook server")
	s.performSync(ctx, runstore.TriggerStartup)

	// Start the SSE broadcaster in the background.
	go s.broadcaster.Run(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", s.handleWebhook)
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/assets/", s.handleAssets)
	mux.HandleFunc("/api/plan", s.handlePlan)
	mux.HandleFunc("/api/", s.handleAPI)

	server := &http.Server{
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		// WriteTimeout applies to all endpoints. Long-lived SSE connections on
		// GET /api/events explicitly clear their write deadline per-connection
		// via http.ResponseController inside handleEvents.
		WriteTimeout:   30 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MB
	}

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("webhook server starting", "addr", listener.Addr().String())
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		s.logger.Info("shutting down webhook server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// handleWebhook handles incoming GitHub webhook requests
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		s.logger.Warn("rejecting non-POST request", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check content type
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		s.logger.Warn("rejecting request with invalid content type", "content_type", contentType)
		http.Error(w, "Invalid content type", http.StatusBadRequest)
		return
	}

	// Read body
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB limit
	if err != nil {
		s.logger.Error("failed to read request body", "error", err)
		http.Error(w, "Failed to read body", http.StatusInternalServerError)
		return
	}
	defer func() {
		_ = r.Body.Close()
	}()

	// Verify signature
	signature := r.Header.Get("X-Hub-Signature-256")
	if !s.verifySignature(body, signature) {
		s.logger.Warn("rejecting request with invalid signature")
		http.Error(w, "Invalid signature", http.StatusForbidden)
		return
	}

	// Parse event type
	eventType := r.Header.Get("X-GitHub-Event")
	s.logger.Info("received webhook", "event", eventType)

	// Check if event type is allowed
	if !s.isEventTypeAllowed(eventType) {
		s.logger.Info("ignoring disallowed event type", "event", eventType)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "Event type not configured for sync\n")
		return
	}

	// Parse push event
	var event GitHubPushEvent
	if err := json.Unmarshal(body, &event); err != nil {
		s.logger.Error("failed to parse webhook payload", "error", err)
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	// Check if ref is allowed (global filter)
	if !s.isRefAllowed(event.Ref) {
		s.logger.Info("ignoring disallowed ref", "ref", event.Ref)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "Ref not configured for sync\n")
		return
	}

	// Check if the push matches a configured repository and tracked ref
	if !s.matchesConfiguredRepo(event) {
		s.logger.Info("ignoring webhook for unconfigured repository/ref",
			"repo", event.Repository.FullName,
			"ref", event.Ref)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "Repository/ref not configured for sync\n")
		return
	}

	s.logger.Info("webhook accepted",
		"event", eventType,
		"ref", event.Ref,
		"commit", event.After,
		"repo", event.Repository.FullName)

	// Trigger debounced sync
	s.debounce.trigger(func() {
		s.performSync(context.Background(), runstore.TriggerWebhook)
	})

	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "Sync triggered\n")
}

// handleRoot serves the Web UI SPA at the root path.
// Currently returns a placeholder response; will serve the UI in future.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	// Only handle exact root path and GET/HEAD methods
	if r.URL.Path != "/" {
		// Unknown paths should serve the UI entry for client-side routing
		// For now, return 404; will serve UI index.html in future
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "<!DOCTYPE html><html><head><title>quadsyncd</title></head><body><h1>quadsyncd Web UI</h1><p>Placeholder for Web UI MVP</p></body></html>\n")
}

// handleAssets serves static assets for the Web UI.
// Currently returns a placeholder response; will serve actual assets in future.
func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Placeholder: return 404 for all assets until Web UI is implemented
	http.NotFound(w, r)
}

// convertSyncPlanToRunstorePlan converts a sync.Plan to runstore.Plan format.
func convertSyncPlanToRunstorePlan(syncPlan *quadsyncd.Plan, conflicts []runstore.ConflictSummary) runstore.Plan {
	ops := make([]runstore.PlanOp, 0, len(syncPlan.Add)+len(syncPlan.Update)+len(syncPlan.Delete))

	// Convert Add operations
	for _, op := range syncPlan.Add {
		ops = append(ops, runstore.PlanOp{
			Op:         "add",
			Path:       op.DestPath,
			SourceRepo: op.SourceRepo,
			SourceRef:  op.SourceRef,
			SourceSHA:  op.SourceSHA,
		})
	}

	// Convert Update operations
	for _, op := range syncPlan.Update {
		ops = append(ops, runstore.PlanOp{
			Op:         "update",
			Path:       op.DestPath,
			SourceRepo: op.SourceRepo,
			SourceRef:  op.SourceRef,
			SourceSHA:  op.SourceSHA,
		})
	}

	// Convert Delete operations
	for _, op := range syncPlan.Delete {
		ops = append(ops, runstore.PlanOp{
			Op:   "delete",
			Path: op.DestPath,
		})
	}

	return runstore.Plan{
		Requested: runstore.PlanRequest{}, // UI-triggered plans don't specify a request scope
		Conflicts: conflicts,
		Ops:       ops,
	}
}

// handlePlan handles UI-triggered plan execution (dry-run sync).
// POST /api/plan triggers a plan operation that creates a run record with plan.json.
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	ctx := r.Context()

	// Create initial run metadata for plan
	meta := &runstore.RunMeta{
		Kind:      runstore.RunKindPlan,
		Trigger:   runstore.TriggerUI,
		StartedAt: time.Now().UTC(),
		Status:    runstore.RunStatusRunning,
		DryRun:    true,
		Revisions: make(map[string]string),
		Conflicts: []runstore.ConflictSummary{},
	}

	if err := s.store.Create(ctx, meta); err != nil {
		s.logger.Error("failed to create plan run record", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create plan record"})
		return
	}

	s.logger.Info("created plan run record", "run_id", meta.ID)

	// Create a tee logger that writes to both console and runstore
	var ndjsonLevel slog.Level = slog.LevelInfo
	if leveler, ok := s.logger.Handler().(interface{ Level() slog.Level }); ok {
		ndjsonLevel = leveler.Level()
	}

	ndjsonHandler := logging.NewNDJSONHandler(func(line []byte) error {
		return s.store.AppendLog(ctx, meta.ID, line)
	}, &logging.NDJSONHandlerOptions{
		Level: ndjsonLevel,
	})

	teeHandler := logging.NewTeeHandler(s.logger.Handler(), ndjsonHandler)
	logger := slog.New(teeHandler)

	// Run sync in dry-run mode (plan)
	logger.Info("performing plan operation")
	engine := quadsyncd.NewEngineWithFactory(s.cfg, quadsyncd.GitClientFactory(s.gitFactory), s.systemd, logger, true)
	result, planErr := engine.Run(ctx)

	// Finalize run metadata
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

	// Persist plan.json for plan runs
	if result != nil && result.Plan != nil {
		planData := convertSyncPlanToRunstorePlan(result.Plan, meta.Conflicts)
		if err := s.store.WritePlan(ctx, meta.ID, planData); err != nil {
			logger.Error("failed to persist plan.json", "error", err)
		}
	}

	// Update run metadata with final state
	if err := s.store.Update(ctx, meta); err != nil {
		logger.Error("failed to update plan run record", "error", err)
	}

	// Return plan run ID in response
	w.Header().Set("Content-Type", "application/json")
	if planErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		resp := map[string]interface{}{
			"error":  planErr.Error(),
			"run_id": meta.ID,
		}
		_ = json.NewEncoder(w).Encode(resp)
	} else {
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"run_id": meta.ID,
			"status": "success",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// handleAPI routes JSON API requests to the appropriate handler.
// Unknown endpoints return HTTP 501 Not Implemented.
func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch path {
	case "/api/overview":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		s.handleOverview(w, r)
		return
	case "/api/runs":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		s.handleRuns(w, r)
		return
	case "/api/units":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		s.handleUnits(w, r)
		return
	case "/api/timer":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		s.handleTimer(w, r)
		return
	case "/api/events":
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		s.handleEvents(w, r)
		return
	}

	// Routes under /api/runs/{id}[/logs|/plan]
	if rest, ok := strings.CutPrefix(path, "/api/runs/"); ok && rest != "" {
		if id, ok2 := strings.CutSuffix(rest, "/logs"); ok2 && id != "" && !strings.Contains(id, "/") {
			if r.Method != http.MethodGet {
				writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
				return
			}
			s.handleRunLogs(w, r, id)
			return
		}
		if id, ok2 := strings.CutSuffix(rest, "/plan"); ok2 && id != "" && !strings.Contains(id, "/") {
			if r.Method != http.MethodGet {
				writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
				return
			}
			s.handleRunPlan(w, r, id)
			return
		}
		if !strings.Contains(rest, "/") {
			if r.Method != http.MethodGet {
				writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
				return
			}
			s.handleRunDetail(w, r, rest)
			return
		}
	}

	// Fallback: not implemented
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = fmt.Fprintf(w, `{"error":"API endpoint not implemented"}`+"\n")
}

// OverviewRepo describes a configured repository in the overview response.
type OverviewRepo struct {
	URL string `json:"url"`
	Ref string `json:"ref,omitempty"`
	SHA string `json:"sha,omitempty"`
}

// OverviewResponse is the response shape for GET /api/overview.
type OverviewResponse struct {
	Repositories  []OverviewRepo `json:"repositories"`
	LastRunID     string         `json:"last_run_id,omitempty"`
	LastRunStatus string         `json:"last_run_status,omitempty"`
}

// handleOverview serves GET /api/overview.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	repos := s.cfg.EffectiveRepositories()

	// Best-effort: read state.json for current per-repo SHAs.
	state, err := loadSyncState(s.cfg.StateFilePath())
	if err != nil {
		s.logger.Warn("failed to load sync state for overview", "error", err)
	}

	overviewRepos := make([]OverviewRepo, len(repos))
	for i, spec := range repos {
		or := OverviewRepo{URL: spec.URL, Ref: spec.Ref}
		if state.Revisions != nil {
			if sha, ok := state.Revisions[spec.URL]; ok {
				or.SHA = sha
			}
		}
		// Backward-compat: single-repo state may only have Commit set.
		if or.SHA == "" && state.Commit != "" && len(repos) == 1 {
			or.SHA = state.Commit
		}
		overviewRepos[i] = or
	}

	resp := OverviewResponse{Repositories: overviewRepos}

	// Best-effort: attach last run info.
	if runs, err := s.store.List(ctx); err == nil && len(runs) > 0 {
		resp.LastRunID = runs[0].ID
		resp.LastRunStatus = string(runs[0].Status)
	}

	writeJSON(w, http.StatusOK, resp)
}

// RunsListResponse is the response shape for GET /api/runs.
type RunsListResponse struct {
	Items      []runstore.RunMeta `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

// handleRuns serves GET /api/runs?limit=&cursor=.
// Default limit is 20; maximum is 100. Logs use a higher limit (see handleRunLogs).
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}
	offset := decodeCursor(r.URL.Query().Get("cursor"))

	runs, err := s.store.List(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list runs")
		return
	}

	items, nextCursor := paginateSlice(runs, offset, limit)
	writeJSON(w, http.StatusOK, RunsListResponse{Items: items, NextCursor: nextCursor})
}

// handleRunDetail serves GET /api/runs/{id}.
func (s *Server) handleRunDetail(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

	meta, err := s.store.Get(ctx, id)
	if err != nil {
		if isNotFoundErr(err) {
			writeJSONError(w, http.StatusNotFound, "run not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "failed to get run")
		return
	}

	writeJSON(w, http.StatusOK, meta)
}

// RunLogsResponse is the response shape for GET /api/runs/{id}/logs.
type RunLogsResponse struct {
	Items      []map[string]interface{} `json:"items"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

// handleRunLogs serves GET /api/runs/{id}/logs?level=&component=&q=&since=&limit=&cursor=.
// Default limit is 100; maximum is 1000. A higher limit than /api/runs is used because
// a single run can produce many log lines.
func (s *Server) handleRunLogs(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

	// Validate run exists.
	if _, err := s.store.Get(ctx, id); err != nil {
		if isNotFoundErr(err) {
			writeJSONError(w, http.StatusNotFound, "run not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "failed to get run")
		return
	}

	q := r.URL.Query()
	levelFilter := strings.ToLower(q.Get("level"))
	componentFilter := q.Get("component")
	qFilter := q.Get("q")
	sinceStr := q.Get("since")

	limit := 100
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}
	offset := decodeCursor(q.Get("cursor"))

	// Parse optional since timestamp.
	var sinceTime time.Time
	if sinceStr != "" {
		if t, err := time.Parse(time.RFC3339Nano, sinceStr); err == nil {
			sinceTime = t
		} else if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			sinceTime = t
		}
	}

	records, err := s.store.ReadLog(ctx, id)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to read logs")
		return
	}

	// Apply filters.
	filtered := make([]map[string]interface{}, 0, len(records))
	for _, rec := range records {
		if levelFilter != "" {
			lvl, _ := rec["level"].(string)
			if strings.ToLower(lvl) != levelFilter {
				continue
			}
		}
		if componentFilter != "" {
			comp, _ := rec["component"].(string)
			if !strings.Contains(comp, componentFilter) {
				continue
			}
		}
		if qFilter != "" {
			msg, _ := rec["msg"].(string)
			if !strings.Contains(msg, qFilter) {
				continue
			}
		}
		if !sinceTime.IsZero() {
			if timeStr, ok := rec["time"].(string); ok {
				var t time.Time
				if pt, err := time.Parse(time.RFC3339Nano, timeStr); err == nil {
					t = pt
				} else if pt, err := time.Parse(time.RFC3339, timeStr); err == nil {
					t = pt
				}
				if !t.IsZero() && !t.After(sinceTime) {
					continue
				}
			}
		}
		filtered = append(filtered, rec)
	}

	items, nextCursor := paginateSlice(filtered, offset, limit)
	if items == nil {
		items = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, RunLogsResponse{Items: items, NextCursor: nextCursor})
}

// handleRunPlan serves GET /api/runs/{id}/plan.
func (s *Server) handleRunPlan(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

	// Validate run exists first.
	if _, err := s.store.Get(ctx, id); err != nil {
		if isNotFoundErr(err) {
			writeJSONError(w, http.StatusNotFound, "run not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "failed to get run")
		return
	}

	plan, err := s.store.ReadPlan(ctx, id)
	if err != nil {
		if errors.Is(err, runstore.ErrPlanNotFound) {
			writeJSONError(w, http.StatusNotFound, "plan not found for run")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "failed to read plan")
		return
	}

	writeJSON(w, http.StatusOK, plan)
}

// UnitInfo describes a single managed quadlet unit.
type UnitInfo struct {
	Name       string `json:"name"`
	SourcePath string `json:"source_path"`
	SourceRepo string `json:"source_repo,omitempty"`
	SourceRef  string `json:"source_ref,omitempty"`
	SourceSHA  string `json:"source_sha,omitempty"`
	Hash       string `json:"hash"`
}

// UnitsResponse is the response shape for GET /api/units.
type UnitsResponse struct {
	Items []UnitInfo `json:"items"`
}

// handleUnits serves GET /api/units.
func (s *Server) handleUnits(w http.ResponseWriter, _ *http.Request) {
	state, err := loadSyncState(s.cfg.StateFilePath())
	if err != nil {
		s.logger.Warn("failed to load sync state for units", "error", err)
	}

	items := make([]UnitInfo, 0, len(state.ManagedFiles))
	for destPath, mf := range state.ManagedFiles {
		items = append(items, UnitInfo{
			Name:       quadlet.UnitNameFromQuadlet(destPath),
			SourcePath: mf.SourcePath,
			SourceRepo: mf.SourceRepo,
			SourceRef:  mf.SourceRef,
			SourceSHA:  mf.SourceSHA,
			Hash:       mf.Hash,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })

	writeJSON(w, http.StatusOK, UnitsResponse{Items: items})
}

// TimerInfo is the response shape for GET /api/timer.
type TimerInfo struct {
	Unit   string `json:"unit"`
	Active bool   `json:"active"`
}

// handleTimer serves GET /api/timer.
// It queries the systemd user timer unit status on a best-effort basis.
func (s *Server) handleTimer(w http.ResponseWriter, r *http.Request) {
	const timerUnit = "quadsyncd-sync.timer"
	info := TimerInfo{Unit: timerUnit}

	ctxTimeout, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	status, _ := s.systemd.GetUnitStatus(ctxTimeout, timerUnit)
	info.Active = status == "active"

	writeJSON(w, http.StatusOK, info)
}

// handleEvents serves GET /api/events as a Server-Sent Events stream.
// It streams run lifecycle events (run_started, run_updated, log_appended, plan_ready)
// in real-time by subscribing to the disk-backed Broadcaster.
//
// The write deadline is cleared per-connection via http.ResponseController so the
// 30s server-level WriteTimeout does not terminate long-lived SSE connections.
// Keep-alive comments (": ping") are sent every 15 seconds to prevent proxy timeouts.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	// Set SSE headers before the first write commits the status code.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Prevent nginx/caddy from buffering the stream.
	w.Header().Set("X-Accel-Buffering", "no")

	// Clear the per-connection write deadline so the connection is not cut
	// by the server-level WriteTimeout (which is 0, but belt-and-suspenders).
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	// Send initial keep-alive comment; this also flushes the response headers.
	_, _ = fmt.Fprintf(w, ": ping\n\n")
	_ = rc.Flush()

	ch := s.broadcaster.subscribe()
	defer s.broadcaster.unsubscribe(ch)

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, err := formatSSEEvent(ev)
			if err != nil {
				s.logger.Warn("failed to format SSE event", "kind", ev.kind, "error", err)
				continue
			}
			if _, err := w.Write(b); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		case <-keepAlive.C:
			if _, err := fmt.Fprintf(w, ": ping\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}

// writeJSON encodes v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// encodeCursor encodes an integer offset as an opaque cursor string.
func encodeCursor(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// decodeCursor decodes an opaque cursor string into an integer offset.
// Returns 0 for an empty or invalid cursor.
func decodeCursor(cursor string) int {
	if cursor == "" {
		return 0
	}
	b, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(string(b))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// paginateSlice returns a page of items starting at offset and the next cursor (empty if no more pages).
func paginateSlice[T any](items []T, offset, limit int) ([]T, string) {
	if offset >= len(items) {
		return []T{}, ""
	}
	end := offset + limit
	var (
		nextCursor string
		page       []T
	)
	if end < len(items) {
		nextCursor = encodeCursor(end)
		page = items[offset:end]
	} else {
		page = items[offset:]
	}
	return page, nextCursor
}

// loadSyncState reads state.json from the given path.
// Returns a zero-value State on any read or parse error.
func loadSyncState(stateFilePath string) (quadsyncd.State, error) {
	data, err := os.ReadFile(stateFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return quadsyncd.State{ManagedFiles: make(map[string]quadsyncd.ManagedFile)}, nil
		}
		return quadsyncd.State{}, fmt.Errorf("failed to read state file: %w", err)
	}
	var state quadsyncd.State
	if err := json.Unmarshal(data, &state); err != nil {
		return quadsyncd.State{}, fmt.Errorf("failed to parse state file: %w", err)
	}
	if state.ManagedFiles == nil {
		state.ManagedFiles = make(map[string]quadsyncd.ManagedFile)
	}
	return state, nil
}

// isNotFoundErr reports whether err indicates a missing or invalid run.
func isNotFoundErr(err error) bool {
	return errors.Is(err, runstore.ErrRunNotFound)
}

// verifySignature verifies the GitHub webhook signature
func (s *Server) verifySignature(body []byte, signature string) bool {
	if signature == "" {
		return false
	}

	// GitHub signature format: sha256=<hex>
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	signature = strings.TrimPrefix(signature, "sha256=")

	// Compute expected signature
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	// Constant-time comparison
	return hmac.Equal([]byte(signature), []byte(expected))
}

// isEventTypeAllowed checks if the event type is in the allowed list
func (s *Server) isEventTypeAllowed(eventType string) bool {
	return len(s.cfg.Serve.AllowedEventTypes) == 0 || sliceContains(s.cfg.Serve.AllowedEventTypes, eventType)
}

// isRefAllowed checks if the ref is in the allowed list
func (s *Server) isRefAllowed(ref string) bool {
	return len(s.cfg.Serve.AllowedRefs) == 0 || sliceContains(s.cfg.Serve.AllowedRefs, ref)
}

// matchesConfiguredRepo checks if the push event matches at least one configured
// repository (by URL) with a matching tracked ref. This ensures that only pushes
// to refs actually being synced trigger a sync operation.
func (s *Server) matchesConfiguredRepo(event GitHubPushEvent) bool {
	repos := s.cfg.EffectiveRepositories()
	for _, spec := range repos {
		if repoURLMatchesEvent(spec.URL, event) && spec.Ref == event.Ref {
			return true
		}
	}
	return false
}

// repoURLMatchesEvent reports whether a configured repo URL corresponds to the
// repository that sent the webhook event. It compares the normalised full name
// (owner/repo) extracted from the configured URL against the event's FullName,
// CloneURL and SSHURL fields.
func repoURLMatchesEvent(cfgURL string, event GitHubPushEvent) bool {
	cfgName := repoFullNameFromURL(cfgURL)
	if cfgName == "" {
		return false
	}
	if cfgName == event.Repository.FullName {
		return true
	}
	if event.Repository.CloneURL != "" && cfgName == repoFullNameFromURL(event.Repository.CloneURL) {
		return true
	}
	if event.Repository.SSHURL != "" && cfgName == repoFullNameFromURL(event.Repository.SSHURL) {
		return true
	}
	return false
}

// repoFullNameFromURL extracts the "owner/repo" path from a Git remote URL.
// It supports HTTPS, SSH scheme, and SSH shorthand (git@host:owner/repo) URLs.
func repoFullNameFromURL(rawURL string) string {
	// Handle SSH shorthand: git@github.com:org/repo.git
	if strings.HasPrefix(rawURL, "git@") {
		if idx := strings.Index(rawURL, ":"); idx >= 0 {
			return strings.TrimSuffix(rawURL[idx+1:], ".git")
		}
		return ""
	}

	// Handle scheme-based URLs (https://, ssh://, http://)
	withoutScheme := rawURL
	if idx := strings.Index(rawURL, "://"); idx >= 0 {
		withoutScheme = rawURL[idx+3:]
	}

	// Remove user info (e.g. git@ in ssh://git@host/path)
	if at := strings.Index(withoutScheme, "@"); at >= 0 {
		withoutScheme = withoutScheme[at+1:]
	}

	// Skip host, return path
	if slash := strings.Index(withoutScheme, "/"); slash >= 0 {
		path := withoutScheme[slash+1:]
		return strings.TrimSuffix(path, ".git")
	}

	return ""
}

// sliceContains reports whether s is present in the slice.
func sliceContains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// performSync executes the sync operation with single-flight semantics and runstore instrumentation.
// If a sync is already in progress, at most one additional run is queued;
// further concurrent requests are dropped to avoid unbounded goroutine pile-up.
func (s *Server) performSync(ctx context.Context, trigger runstore.TriggerSource) {
	s.syncMu.Lock()
	if s.syncRunning {
		s.syncPending = true
		s.syncMu.Unlock()
		s.logger.Info("sync already in progress, queuing pending re-run")
		return
	}
	s.syncRunning = true
	s.syncMu.Unlock()

	runCtx := ctx
	for {
		s.executeInstrumentedSync(runCtx, trigger)

		// Atomically check whether another sync was requested while we were
		// running. If not, release the running slot and stop; if yes, clear
		// the flag and loop to service that one pending request.
		s.syncMu.Lock()
		if !s.syncPending {
			s.syncRunning = false
			s.syncMu.Unlock()
			break
		}
		s.syncPending = false
		s.syncMu.Unlock()

		// The pending request arrived independently of the original caller;
		// use a fresh background context so that cancellation of the initial
		// context (e.g. server shutdown signalled after the first sync was
		// already queued) does not abort the re-run.
		runCtx = context.Background()
		s.logger.Info("re-running sync due to pending request")
	}
}

// executeInstrumentedSync performs a single sync with run record creation and logging.
func (s *Server) executeInstrumentedSync(ctx context.Context, trigger runstore.TriggerSource) {
	// Create initial run metadata
	meta := &runstore.RunMeta{
		Kind:      runstore.RunKindSync,
		Trigger:   trigger,
		StartedAt: time.Now().UTC(),
		Status:    runstore.RunStatusRunning,
		DryRun:    false,
		Revisions: make(map[string]string),
		Conflicts: []runstore.ConflictSummary{},
	}

	// Try to create run record; if it fails, continue without instrumentation
	runRecordCreated := false
	if err := s.store.Create(ctx, meta); err != nil {
		s.logger.Error("failed to create run record, continuing without instrumentation", "error", err)
		// Run sync without runstore instrumentation
		engine := quadsyncd.NewEngineWithFactory(s.cfg, quadsyncd.GitClientFactory(s.gitFactory), s.systemd, s.logger, false)
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

	// Create a tee logger that writes to both console and runstore
	// Use the same level as the console logger
	var ndjsonLevel slog.Level = slog.LevelInfo
	if leveler, ok := s.logger.Handler().(interface{ Level() slog.Level }); ok {
		ndjsonLevel = leveler.Level()
	}

	ndjsonHandler := logging.NewNDJSONHandler(func(line []byte) error {
		return s.store.AppendLog(ctx, meta.ID, line)
	}, &logging.NDJSONHandlerOptions{
		Level: ndjsonLevel,
	})

	teeHandler := logging.NewTeeHandler(s.logger.Handler(), ndjsonHandler)
	logger := slog.New(teeHandler)

	// Run sync with instrumented logger
	logger.Info("performing sync operation")
	engine := quadsyncd.NewEngineWithFactory(s.cfg, quadsyncd.GitClientFactory(s.gitFactory), s.systemd, logger, false)
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

	// Update run metadata with final state (only if we created the run record)
	if runRecordCreated {
		if err := s.store.Update(ctx, meta); err != nil {
			logger.Error("failed to update run record", "error", err)
		}
	}
}

// trigger schedules the callback to run after the debounce delay
func (d *debouncer) trigger(callback func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.callback = callback

	if d.timer != nil {
		d.timer.Stop()
	}

	d.timer = time.AfterFunc(d.delay, func() {
		d.mu.Lock()
		cb := d.callback
		d.mu.Unlock()

		if cb != nil {
			cb()
		}
	})
}
