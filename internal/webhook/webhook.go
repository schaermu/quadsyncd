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
	"strings"
	"sync"
	"time"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/git"
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
func NewServer(cfg *config.Config, gitFactory GitClientFactory, systemd systemduser.Systemd, logger *slog.Logger) (*Server, error) {
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
	s.performSync(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", s.handleWebhook)
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/assets/", s.handleAssets)
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
		s.performSync(context.Background())
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

// handleAPI handles JSON API requests.
// Currently returns HTTP 501 Not Implemented for all endpoints.
func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = fmt.Fprintf(w, `{"error":"API endpoint not implemented"}`+"\n")
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

// performSync executes the sync operation with single-flight semantics.
// If a sync is already in progress, at most one additional run is queued;
// further concurrent requests are dropped to avoid unbounded goroutine pile-up.
func (s *Server) performSync(ctx context.Context) {
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
		s.logger.Info("performing sync operation")

		engine := quadsyncd.NewEngineWithFactory(s.cfg, quadsyncd.GitClientFactory(s.gitFactory), s.systemd, s.logger, false)
		_, err := engine.Run(runCtx)
		if err != nil {
			s.logger.Error("sync failed", "error", err)
		} else {
			s.logger.Info("sync completed successfully")
		}

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
