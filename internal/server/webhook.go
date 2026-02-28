package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/schaermu/quadsyncd/internal/runstore"
)

// GitHubPushEvent represents the relevant fields from a GitHub push webhook payload.
type GitHubPushEvent struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Repository struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
		SSHURL   string `json:"ssh_url"`
	} `json:"repository"`
}

// debouncer implements debouncing for webhook events.
type debouncer struct {
	mu       sync.Mutex
	timer    *time.Timer
	delay    time.Duration
	callback func()
}

// trigger schedules the callback to run after the debounce delay.
// Repeated calls within the delay window reset the timer.
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

// handleWebhook handles incoming GitHub webhook requests.
// Webhook error responses use http.Error (plain text) intentionally.
// GitHub does not parse JSON error bodies from webhook endpoints,
// and plain text is simpler to debug in webhook delivery logs.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		s.logger.Warn("rejecting non-POST request", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check content type
	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "application/json") {
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
		s.syncSvc.TriggerSync(context.Background(), runstore.TriggerWebhook)
	})

	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "Sync triggered\n")
}

// verifySignature verifies the GitHub webhook HMAC-SHA256 signature.
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

// isEventTypeAllowed checks if the event type is in the allowed list.
func (s *Server) isEventTypeAllowed(eventType string) bool {
	return len(s.cfg.Serve.AllowedEventTypes) == 0 || sliceContains(s.cfg.Serve.AllowedEventTypes, eventType)
}

// isRefAllowed checks if the ref is in the allowed list.
func (s *Server) isRefAllowed(ref string) bool {
	return len(s.cfg.Serve.AllowedRefs) == 0 || sliceContains(s.cfg.Serve.AllowedRefs, ref)
}

// matchesConfiguredRepo checks if the push event matches at least one configured
// repository (by URL) with a matching tracked ref.
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
// repository that sent the webhook event.
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
