package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
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

// handleAPI routes JSON API requests to specific handlers.
func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Route to specific API handlers
	if path == "/api/overview" {
		s.handleOverview(w, r)
		return
	}
	if path == "/api/runs" {
		s.handleRuns(w, r)
		return
	}
	if strings.HasPrefix(path, "/api/runs/") && strings.HasSuffix(path, "/logs") {
		s.handleRunLogs(w, r)
		return
	}
	if strings.HasPrefix(path, "/api/runs/") && strings.HasSuffix(path, "/plan") {
		s.handleRunPlan(w, r)
		return
	}
	if strings.HasPrefix(path, "/api/runs/") {
		s.handleRunDetail(w, r)
		return
	}
	if path == "/api/units" {
		s.handleUnits(w, r)
		return
	}
	if path == "/api/timer" {
		s.handleTimer(w, r)
		return
	}

	// Unknown API endpoint
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = fmt.Fprintf(w, `{"error":"API endpoint not implemented"}`+"\n")
}

// handleOverview returns an overview of repositories and last run status.
// GET /api/overview
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	ctx := r.Context()

	// Get repositories from config
	repos := s.cfg.EffectiveRepositories()
	type repoInfo struct {
		URL       string  `json:"url"`
		Ref       string  `json:"ref,omitempty"`
		SHA       string  `json:"sha,omitempty"`
		Freshness *string `json:"freshness,omitempty"`
	}

	repoInfos := make([]repoInfo, 0, len(repos))
	for _, repo := range repos {
		info := repoInfo{
			URL: repo.URL,
			Ref: repo.Ref,
		}
		repoInfos = append(repoInfos, info)
	}

	// Get last run to populate SHA and freshness
	runs, err := s.store.List(ctx)
	var lastRunID string
	var lastRunStatus string

	if err == nil && len(runs) > 0 {
		lastRun := runs[0] // Already sorted newest first
		lastRunID = lastRun.ID
		lastRunStatus = string(lastRun.Status)

		// Populate SHA from last run's revisions
		for i := range repoInfos {
			if sha, ok := lastRun.Revisions[repoInfos[i].URL]; ok {
				repoInfos[i].SHA = sha

				// Calculate freshness (best-effort)
				if lastRun.EndedAt != nil {
					age := time.Since(*lastRun.EndedAt)
					freshness := formatFreshness(age)
					repoInfos[i].Freshness = &freshness
				}
			}
		}
	}

	response := map[string]interface{}{
		"repositories": repoInfos,
	}
	if lastRunID != "" {
		response["last_run_id"] = lastRunID
		response["last_run_status"] = lastRunStatus
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

// formatFreshness returns a human-readable freshness string.
func formatFreshness(age time.Duration) string {
	if age < time.Minute {
		return "just now"
	}
	if age < time.Hour {
		mins := int(age.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	}
	if age < 24*time.Hour {
		hours := int(age.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}
	days := int(age.Hours() / 24)
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
}

// handleRuns returns a paginated list of runs.
// GET /api/runs?limit=N&cursor=CURSOR
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	ctx := r.Context()

	// Parse query parameters
	query := r.URL.Query()
	limit := 50 // default
	if limitStr := query.Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil {
			if parsedLimit > 0 && parsedLimit <= 100 {
				limit = parsedLimit
			}
		}
	}

	cursor := query.Get("cursor")

	// Get all runs
	runs, err := s.store.List(ctx)
	if err != nil {
		s.logger.Error("failed to list runs", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to list runs"})
		return
	}

	// Apply cursor filtering (cursor is the run ID to start after)
	startIdx := 0
	if cursor != "" {
		for i, run := range runs {
			if run.ID == cursor {
				startIdx = i + 1
				break
			}
		}
	}

	// Apply pagination
	endIdx := startIdx + limit
	if endIdx > len(runs) {
		endIdx = len(runs)
	}

	pageRuns := runs[startIdx:endIdx]

	// Build response with summary items
	items := make([]map[string]interface{}, len(pageRuns))
	for i, run := range pageRuns {
		item := map[string]interface{}{
			"id":         run.ID,
			"kind":       run.Kind,
			"trigger":    run.Trigger,
			"status":     run.Status,
			"started_at": run.StartedAt.Format(time.RFC3339Nano),
			"dry_run":    run.DryRun,
		}
		if run.EndedAt != nil {
			item["ended_at"] = run.EndedAt.Format(time.RFC3339Nano)
		}
		if run.Error != "" {
			item["error"] = run.Error
		}
		items[i] = item
	}

	response := map[string]interface{}{
		"items": items,
	}

	// Add next_cursor if there are more results
	if endIdx < len(runs) {
		response["next_cursor"] = runs[endIdx-1].ID
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

// handleRunDetail returns full metadata for a specific run.
// GET /api/runs/{id}
func (s *Server) handleRunDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	// Extract run ID from path: /api/runs/{id}
	path := r.URL.Path
	id := strings.TrimPrefix(path, "/api/runs/")
	if id == "" || id == path {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Missing run ID"})
		return
	}

	ctx := r.Context()
	meta, err := s.store.Get(ctx, id)
	if err != nil {
		s.logger.Warn("failed to get run", "id", id, "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Run not found"})
		return
	}

	// Return the full meta.json
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(meta)
}

// handleRunLogs returns paginated and filtered logs for a specific run.
// GET /api/runs/{id}/logs?level=&component=&q=&since=&limit=&cursor=
func (s *Server) handleRunLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	// Extract run ID from path: /api/runs/{id}/logs
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api/runs/")
	path = strings.TrimSuffix(path, "/logs")
	id := path
	if id == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Missing run ID"})
		return
	}

	ctx := r.Context()

	// Check if run exists first
	if _, err := s.store.Get(ctx, id); err != nil {
		s.logger.Warn("failed to get run for logs", "id", id, "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Run not found"})
		return
	}

	// Parse query parameters
	query := r.URL.Query()
	levelFilter := query.Get("level")
	componentFilter := query.Get("component")
	searchQuery := query.Get("q")
	sinceStr := query.Get("since")
	cursorStr := query.Get("cursor")

	limit := 100 // default
	if limitStr := query.Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil {
			if parsedLimit > 0 && parsedLimit <= 1000 {
				limit = parsedLimit
			}
		}
	}

	// Parse since timestamp if provided
	var sinceTime time.Time
	if sinceStr != "" {
		if t, err := time.Parse(time.RFC3339Nano, sinceStr); err == nil {
			sinceTime = t
		} else if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			sinceTime = t
		}
	}

	// Parse cursor (line offset)
	cursorOffset := 0
	if cursorStr != "" {
		offset, err := strconv.Atoi(cursorStr)
		if err != nil {
			s.logger.Warn("invalid cursor offset", "cursor", cursorStr, "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid cursor value"})
			return
		}
		if offset < 0 {
			s.logger.Warn("negative cursor offset", "cursor", cursorStr, "offset", offset)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid cursor value"})
			return
		}
		cursorOffset = offset
	}

	// Read logs with pagination (apply offset and limit at the file-read level)
	// Note: We read limit*2 to account for potential filtering, but this is still bounded
	readLimit := limit * 2
	if readLimit > 10000 {
		readLimit = 10000
	}
	logs, err := s.store.ReadLog(ctx, id, cursorOffset, readLimit)
	if err != nil {
		s.logger.Warn("failed to read logs", "id", id, "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Logs not found"})
		return
	}

	// Filter logs
	var filtered []map[string]interface{}
	for _, record := range logs {
		// Level filter
		if levelFilter != "" {
			if level, ok := record["level"].(string); ok {
				if !strings.EqualFold(level, levelFilter) {
					continue
				}
			} else {
				continue
			}
		}

		// Component filter (check if component field contains the filter string)
		if componentFilter != "" {
			if component, ok := record["component"].(string); ok {
				if !strings.Contains(strings.ToLower(component), strings.ToLower(componentFilter)) {
					continue
				}
			} else {
				continue
			}
		}

		// Search query (search in msg field)
		if searchQuery != "" {
			if msg, ok := record["msg"].(string); ok {
				if !strings.Contains(strings.ToLower(msg), strings.ToLower(searchQuery)) {
					continue
				}
			} else {
				continue
			}
		}

		// Since filter (check time field)
		if !sinceTime.IsZero() {
			if timeStr, ok := record["time"].(string); ok {
				if t, err := time.Parse(time.RFC3339Nano, timeStr); err == nil {
					if t.Before(sinceTime) {
						continue
					}
				} else if t, err := time.Parse(time.RFC3339, timeStr); err == nil {
					if t.Before(sinceTime) {
						continue
					}
				}
			}
		}

		filtered = append(filtered, record)
	}

	// Apply pagination with cursor offset
	startIdx := cursorOffset
	if startIdx > len(filtered) {
		startIdx = len(filtered)
	}

	endIdx := startIdx + limit
	if endIdx > len(filtered) {
		endIdx = len(filtered)
	}

	pageRecords := filtered[startIdx:endIdx]

	response := map[string]interface{}{
		"items": pageRecords,
	}

	// Add next_cursor if there are more results
	if endIdx < len(filtered) {
		response["next_cursor"] = fmt.Sprintf("%d", endIdx)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

// handleRunPlan returns the plan.json for a specific run.
// GET /api/runs/{id}/plan
func (s *Server) handleRunPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	// Extract run ID from path: /api/runs/{id}/plan
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/api/runs/")
	path = strings.TrimSuffix(path, "/plan")
	id := path
	if id == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Missing run ID"})
		return
	}

	ctx := r.Context()
	plan, err := s.store.ReadPlan(ctx, id)
	if err != nil {
		s.logger.Warn("failed to read plan", "id", id, "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Plan not found"})
		return
	}

	// Return the plan.json
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(plan)
}

// handleUnits returns a list of managed systemd units.
// GET /api/units
func (s *Server) handleUnits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	ctx := r.Context()

	// Get the last successful run to determine managed units
	runs, err := s.store.List(ctx)
	if err != nil || len(runs) == 0 {
		// No runs yet, return empty list
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"units": []interface{}{}})
		return
	}

	// Find the most recent successful sync run
	var lastSuccessfulRun *runstore.RunMeta
	for i := range runs {
		if runs[i].Kind == runstore.RunKindSync && runs[i].Status == runstore.RunStatusSuccess {
			lastSuccessfulRun = &runs[i]
			break
		}
	}

	if lastSuccessfulRun == nil {
		// No successful sync yet
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"units": []interface{}{}})
		return
	}

	// Read state file to get managed files
	stateFile := filepath.Join(s.cfg.Paths.StateDir, "state.json")
	stateData, err := os.ReadFile(stateFile)
	if err != nil {
		s.logger.Warn("failed to read state file", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"units": []interface{}{}})
		return
	}

	var state struct {
		ManagedFiles map[string]struct {
			SourcePath string `json:"source_path"`
			Hash       string `json:"hash"`
			SourceRepo string `json:"source_repo"`
			SourceRef  string `json:"source_ref"`
			SourceSHA  string `json:"source_sha"`
		} `json:"managed_files"`
	}

	if err := json.Unmarshal(stateData, &state); err != nil {
		s.logger.Warn("failed to parse state file", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"units": []interface{}{}})
		return
	}

	// Build unit list from map
	units := make([]map[string]interface{}, 0, len(state.ManagedFiles))
	for destPath, file := range state.ManagedFiles {
		unit := map[string]interface{}{
			"name":        quadlet.UnitNameFromQuadlet(destPath),
			"path":        destPath,
			"source_repo": file.SourceRepo,
			"source_ref":  file.SourceRef,
			"source_sha":  file.SourceSHA,
		}
		units = append(units, unit)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"units": units})
}

// handleTimer returns timer status information.
// GET /api/timer
func (s *Server) handleTimer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	// Try to get timer status via systemctl
	// This is best-effort - if systemctl is not available or timer is not installed,
	// we return minimal information
	response := map[string]interface{}{
		"available": false,
	}

	ctx := r.Context()
	timerUnit := "quadsyncd-sync.timer"

	// Check if systemd is available
	available, err := s.systemd.IsAvailable(ctx)
	if err != nil || !available {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	// Try to get timer status using systemctl show
	properties, err := s.systemd.Show(ctx, timerUnit, []string{
		"ActiveState",
		"SubState",
		"UnitFileState",
		"NextElapseUSecRealtime",
	})
	if err != nil {
		// Timer not installed or not available
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	response["available"] = true
	response["unit"] = timerUnit
	if state, ok := properties["ActiveState"]; ok {
		response["active_state"] = state
	}
	if substate, ok := properties["SubState"]; ok {
		response["sub_state"] = substate
	}
	if fileState, ok := properties["UnitFileState"]; ok {
		response["unit_file_state"] = fileState
	}

	// Parse next elapse time (microseconds since epoch)
	if nextStr, ok := properties["NextElapseUSecRealtime"]; ok && nextStr != "0" && nextStr != "" {
		var usecVal int64
		if _, err := fmt.Sscanf(nextStr, "%d", &usecVal); err == nil {
			nextTime := time.Unix(0, usecVal*1000)
			response["next_run"] = nextTime.Format(time.RFC3339Nano)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
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
