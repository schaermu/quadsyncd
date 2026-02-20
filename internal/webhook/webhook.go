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
	} `json:"repository"`
}

// Server implements the webhook HTTP server
type Server struct {
	cfg         *config.Config
	git         git.Client
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
func NewServer(cfg *config.Config, gitClient git.Client, systemd systemduser.Systemd, logger *slog.Logger) (*Server, error) {
	// Load webhook secret from file
	secret, err := os.ReadFile(cfg.Serve.GitHubWebhookSecretFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read webhook secret: %w", err)
	}

	// Trim any whitespace/newlines from secret
	secret = []byte(strings.TrimSpace(string(secret)))

	s := &Server{
		cfg:     cfg,
		git:     gitClient,
		systemd: systemd,
		logger:  logger,
		secret:  secret,
	}

	// Initialize debouncer with 2 second delay
	s.debounce = &debouncer{
		delay: 2 * time.Second,
	}

	return s, nil
}

// Start starts the webhook HTTP server, performing an initial sync first.
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("performing initial sync before starting webhook server")
	s.performSync(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleWebhook)

	server := &http.Server{
		Addr:              s.cfg.Serve.ListenAddr,
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
		s.logger.Info("webhook server starting", "addr", s.cfg.Serve.ListenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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

	// Check if ref is allowed
	if !s.isRefAllowed(event.Ref) {
		s.logger.Info("ignoring disallowed ref", "ref", event.Ref)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "Ref not configured for sync\n")
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
	if len(s.cfg.Serve.AllowedEventTypes) == 0 {
		return true // no filter configured
	}

	for _, allowed := range s.cfg.Serve.AllowedEventTypes {
		if eventType == allowed {
			return true
		}
	}
	return false
}

// isRefAllowed checks if the ref is in the allowed list
func (s *Server) isRefAllowed(ref string) bool {
	if len(s.cfg.Serve.AllowedRefs) == 0 {
		return true // no filter configured
	}

	for _, allowed := range s.cfg.Serve.AllowedRefs {
		if ref == allowed {
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

	for {
		s.logger.Info("performing sync operation")

		engine := quadsyncd.NewEngine(s.cfg, s.git, s.systemd, s.logger, false)
		if err := engine.Run(ctx); err != nil {
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
