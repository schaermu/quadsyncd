// Package webhook provides the HTTP server for GitHub webhooks and the Web UI API.
// This file implements the SSE (Server-Sent Events) broadcaster used by GET /api/events.
//
// Implementation approach: periodic disk scan (no fsnotify).
// Each poll cycle compares on-disk state against last-known state and emits the
// minimal set of events needed to bring clients up to date.
// Delivery semantics: at-least-once, best-effort (duplicates acceptable).
package webhook

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/schaermu/quadsyncd/internal/runstore"
)

// SSE event kind constants.
const (
	sseEventRunStarted  = "run_started"
	sseEventRunUpdated  = "run_updated"
	sseEventLogAppended = "log_appended"
	sseEventPlanReady   = "plan_ready"

	// defaultBroadcastInterval is how often the broadcaster polls for changes.
	defaultBroadcastInterval = 500 * time.Millisecond
)

// SSEEventPayload is the JSON payload carried in every SSE event.
type SSEEventPayload struct {
	RunID     string                     `json:"run_id"`
	Kind      runstore.RunKind           `json:"kind,omitempty"`
	Status    runstore.RunStatus         `json:"status,omitempty"`
	Trigger   runstore.TriggerSource     `json:"trigger,omitempty"`
	DryRun    bool                       `json:"dry_run,omitempty"`
	StartedAt *time.Time                 `json:"started_at,omitempty"`
	EndedAt   *time.Time                 `json:"ended_at,omitempty"`
	Revisions map[string]string          `json:"revisions,omitempty"`
	Conflicts []runstore.ConflictSummary `json:"conflicts,omitempty"`
	Lines     []map[string]interface{}   `json:"lines,omitempty"`
}

// sseEvent is a single event to broadcast to SSE subscribers.
type sseEvent struct {
	kind    string
	payload SSEEventPayload
}

// runTrackState records the last-observed state of a single run directory.
type runTrackState struct {
	status    runstore.RunStatus
	logOffset int64 // byte offset in log.ndjson read so far
	planReady bool  // true once plan.json has been observed
}

// Broadcaster watches the runs directory and fans out SSE events to subscribers.
//
// It uses periodic polling (no fsnotify) for simplicity and portability.
// The poll loop runs in a single goroutine so states is not mutex-protected.
// Only the subscriber map (subs) requires synchronisation with HTTP handler goroutines.
type Broadcaster struct {
	runsDir  string
	subsMu   sync.RWMutex
	subs     map[chan sseEvent]struct{}
	states   map[string]*runTrackState // only accessed by the poll goroutine
	logger   *slog.Logger
	interval time.Duration
}

// newBroadcaster creates a Broadcaster that watches runsDir for run changes.
func newBroadcaster(runsDir string, logger *slog.Logger, interval time.Duration) *Broadcaster {
	return &Broadcaster{
		runsDir:  runsDir,
		subs:     make(map[chan sseEvent]struct{}),
		states:   make(map[string]*runTrackState),
		logger:   logger,
		interval: interval,
	}
}

// subscribe registers a new subscriber and returns a buffered event channel.
func (b *Broadcaster) subscribe() chan sseEvent {
	ch := make(chan sseEvent, 64)
	b.subsMu.Lock()
	b.subs[ch] = struct{}{}
	b.subsMu.Unlock()
	return ch
}

// unsubscribe removes a subscriber and closes its channel.
func (b *Broadcaster) unsubscribe(ch chan sseEvent) {
	b.subsMu.Lock()
	delete(b.subs, ch)
	b.subsMu.Unlock()
	close(ch)
}

// broadcast sends ev to all current subscribers (non-blocking; drops on full buffer).
func (b *Broadcaster) broadcast(ev sseEvent) {
	b.subsMu.RLock()
	defer b.subsMu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			b.logger.Warn("broadcaster: subscriber buffer full, dropping event",
				"kind", ev.kind, "run_id", ev.payload.RunID)
		}
	}
}

// Run starts the polling loop and blocks until ctx is cancelled.
func (b *Broadcaster) Run(ctx context.Context) {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.poll()
		}
	}
}

// poll scans the runs directory and emits events for observed changes.
// Must only be called from the Run goroutine (b.states is not mutex-protected).
func (b *Broadcaster) poll() {
	entries, err := os.ReadDir(b.runsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			b.logger.Warn("broadcaster: failed to read runs dir", "error", err)
		}
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			b.checkRun(entry.Name())
		}
	}
}

// checkRun inspects a single run directory and emits any newly-observed events.
func (b *Broadcaster) checkRun(runID string) {
	runDir := filepath.Join(b.runsDir, runID)

	// meta.json may not exist yet if the run dir was just created; skip silently.
	meta, err := readRunMeta(runDir)
	if err != nil {
		return
	}

	state, known := b.states[runID]
	var events []sseEvent

	if !known {
		// First observation: always emit run_started.
		state = &runTrackState{status: meta.Status}
		b.states[runID] = state
		events = append(events, sseEvent{
			kind:    sseEventRunStarted,
			payload: metaToPayload(runID, meta),
		})
	} else if state.status != meta.Status {
		state.status = meta.Status
		events = append(events, sseEvent{
			kind:    sseEventRunUpdated,
			payload: metaToPayload(runID, meta),
		})
	}

	// Check for new log lines.
	newLines, newOffset := readNewLogLines(filepath.Join(runDir, "log.ndjson"), state.logOffset)
	if len(newLines) > 0 {
		state.logOffset = newOffset
		events = append(events, sseEvent{
			kind: sseEventLogAppended,
			payload: SSEEventPayload{
				RunID: runID,
				Lines: newLines,
			},
		})
	}

	// Check for plan.json appearance.
	if !state.planReady {
		if _, statErr := os.Stat(filepath.Join(runDir, "plan.json")); statErr == nil {
			state.planReady = true
			events = append(events, sseEvent{
				kind:    sseEventPlanReady,
				payload: metaToPayload(runID, meta),
			})
		}
	}

	for _, ev := range events {
		b.broadcast(ev)
	}
}

// readRunMeta reads and parses meta.json from runDir.
func readRunMeta(runDir string) (*runstore.RunMeta, error) {
	data, err := os.ReadFile(filepath.Join(runDir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var meta runstore.RunMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse meta.json in %s: %w", runDir, err)
	}
	return &meta, nil
}

// metaToPayload converts RunMeta to SSEEventPayload.
func metaToPayload(runID string, meta *runstore.RunMeta) SSEEventPayload {
	return SSEEventPayload{
		RunID:     runID,
		Kind:      meta.Kind,
		Status:    meta.Status,
		Trigger:   meta.Trigger,
		DryRun:    meta.DryRun,
		StartedAt: &meta.StartedAt,
		EndedAt:   meta.EndedAt,
		Revisions: meta.Revisions,
		Conflicts: meta.Conflicts,
	}
}

// readNewLogLines reads NDJSON records from path starting at prevOffset.
// Returns new parsed records and the updated byte offset.
func readNewLogLines(path string, prevOffset int64) ([]map[string]interface{}, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, prevOffset
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || info.Size() <= prevOffset {
		return nil, prevOffset
	}

	if _, err := f.Seek(prevOffset, io.SeekStart); err != nil {
		return nil, prevOffset
	}

	var lines []map[string]interface{}
	newOffset := prevOffset
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		raw := scanner.Bytes()
		newOffset += int64(len(raw)) + 1 // +1 for the newline separator
		if len(raw) == 0 {
			continue
		}
		var rec map[string]interface{}
		if err := json.Unmarshal(raw, &rec); err == nil {
			lines = append(lines, rec)
		}
	}
	return lines, newOffset
}

// formatSSEEvent serialises ev into the SSE wire format:
//
//	event: <kind>\n
//	data: <json>\n
//	\n
func formatSSEEvent(ev sseEvent) ([]byte, error) {
	data, err := json.Marshal(ev.payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal SSE payload: %w", err)
	}
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", ev.kind, data)), nil
}
