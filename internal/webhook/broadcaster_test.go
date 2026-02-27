package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schaermu/quadsyncd/internal/runstore"
)

// ---- broadcaster unit tests ----

func newTestBroadcaster(t *testing.T) (*Broadcaster, string) {
	t.Helper()
	runsDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	b := newBroadcaster(runsDir, logger, time.Minute) // long interval; tests call poll() directly
	return b, runsDir
}

// writeTestMeta writes meta.json for a run under runsDir.
func writeTestMeta(t *testing.T, runsDir, runID string, meta runstore.RunMeta) {
	t.Helper()
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "meta.json"), data, 0644); err != nil {
		t.Fatalf("write meta.json: %v", err)
	}
}

func TestBroadcaster_NewRunEmitsRunStarted(t *testing.T) {
	b, runsDir := newTestBroadcaster(t)

	meta := runstore.RunMeta{
		ID:        "20260101-120000-aabbcc",
		Kind:      runstore.RunKindSync,
		Trigger:   runstore.TriggerCLI,
		Status:    runstore.RunStatusRunning,
		StartedAt: time.Now().UTC(),
		Revisions: map[string]string{},
		Conflicts: []runstore.ConflictSummary{},
	}
	writeTestMeta(t, runsDir, meta.ID, meta)

	ch := b.subscribe()
	defer b.unsubscribe(ch)

	b.poll()

	select {
	case ev := <-ch:
		if ev.kind != sseEventRunStarted {
			t.Errorf("expected run_started, got %q", ev.kind)
		}
		if ev.payload.RunID != meta.ID {
			t.Errorf("expected run_id %q, got %q", meta.ID, ev.payload.RunID)
		}
		if ev.payload.Status != runstore.RunStatusRunning {
			t.Errorf("expected status running, got %q", ev.payload.Status)
		}
	default:
		t.Fatal("expected run_started event but got none")
	}
}

func TestBroadcaster_StatusChangeEmitsRunUpdated(t *testing.T) {
	b, runsDir := newTestBroadcaster(t)

	runID := "20260101-120001-bbccdd"
	meta := runstore.RunMeta{
		ID:        runID,
		Kind:      runstore.RunKindSync,
		Trigger:   runstore.TriggerWebhook,
		Status:    runstore.RunStatusRunning,
		StartedAt: time.Now().UTC(),
		Revisions: map[string]string{},
		Conflicts: []runstore.ConflictSummary{},
	}
	writeTestMeta(t, runsDir, runID, meta)

	ch := b.subscribe()
	defer b.unsubscribe(ch)

	// First poll: run_started
	b.poll()
	<-ch // drain run_started

	// Update meta to success
	meta.Status = runstore.RunStatusSuccess
	now := time.Now().UTC()
	meta.EndedAt = &now
	writeTestMeta(t, runsDir, runID, meta)

	// Second poll: run_updated
	b.poll()

	select {
	case ev := <-ch:
		if ev.kind != sseEventRunUpdated {
			t.Errorf("expected run_updated, got %q", ev.kind)
		}
		if ev.payload.Status != runstore.RunStatusSuccess {
			t.Errorf("expected status success, got %q", ev.payload.Status)
		}
	default:
		t.Fatal("expected run_updated event but got none")
	}
}

func TestBroadcaster_LogGrowthEmitsLogAppended(t *testing.T) {
	b, runsDir := newTestBroadcaster(t)

	runID := "20260101-120002-ccddee"
	meta := runstore.RunMeta{
		ID:        runID,
		Kind:      runstore.RunKindSync,
		Status:    runstore.RunStatusRunning,
		StartedAt: time.Now().UTC(),
		Revisions: map[string]string{},
		Conflicts: []runstore.ConflictSummary{},
	}
	writeTestMeta(t, runsDir, runID, meta)

	ch := b.subscribe()
	defer b.unsubscribe(ch)

	b.poll()
	<-ch // drain run_started

	// Write a log line
	logPath := filepath.Join(runsDir, runID, "log.ndjson")
	line := `{"time":"2026-01-01T12:00:00Z","level":"INFO","msg":"syncing"}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	b.poll()

	select {
	case ev := <-ch:
		if ev.kind != sseEventLogAppended {
			t.Errorf("expected log_appended, got %q", ev.kind)
		}
		if len(ev.payload.Lines) == 0 {
			t.Error("expected at least one log line")
		}
	default:
		t.Fatal("expected log_appended event but got none")
	}
}

func TestBroadcaster_PlanJsonEmitsPlanReady(t *testing.T) {
	b, runsDir := newTestBroadcaster(t)

	runID := "20260101-120003-ddeeff"
	meta := runstore.RunMeta{
		ID:        runID,
		Kind:      runstore.RunKindPlan,
		Status:    runstore.RunStatusRunning,
		StartedAt: time.Now().UTC(),
		Revisions: map[string]string{},
		Conflicts: []runstore.ConflictSummary{},
	}
	writeTestMeta(t, runsDir, runID, meta)

	ch := b.subscribe()
	defer b.unsubscribe(ch)

	b.poll()
	<-ch // drain run_started

	// Write plan.json
	planPath := filepath.Join(runsDir, runID, "plan.json")
	if err := os.WriteFile(planPath, []byte(`{"ops":[]}`), 0644); err != nil {
		t.Fatalf("write plan.json: %v", err)
	}

	b.poll()

	select {
	case ev := <-ch:
		if ev.kind != sseEventPlanReady {
			t.Errorf("expected plan_ready, got %q", ev.kind)
		}
		if ev.payload.RunID != runID {
			t.Errorf("expected run_id %q, got %q", runID, ev.payload.RunID)
		}
	default:
		t.Fatal("expected plan_ready event but got none")
	}
}

func TestBroadcaster_NoDuplicateRunStarted(t *testing.T) {
	b, runsDir := newTestBroadcaster(t)

	runID := "20260101-120004-eeffaa"
	meta := runstore.RunMeta{
		ID:        runID,
		Kind:      runstore.RunKindSync,
		Status:    runstore.RunStatusRunning,
		StartedAt: time.Now().UTC(),
		Revisions: map[string]string{},
		Conflicts: []runstore.ConflictSummary{},
	}
	writeTestMeta(t, runsDir, runID, meta)

	ch := b.subscribe()
	defer b.unsubscribe(ch)

	b.poll()
	<-ch // drain run_started

	// Second poll with no changes: no extra events
	b.poll()

	select {
	case ev := <-ch:
		t.Errorf("expected no event on second poll, got %q", ev.kind)
	default:
		// correct: no extra event
	}
}

// ---- SSE framing test ----

func TestFormatSSEEvent(t *testing.T) {
	ev := sseEvent{
		kind: sseEventRunStarted,
		payload: SSEEventPayload{
			RunID:  "test-run-abc",
			Kind:   runstore.RunKindSync,
			Status: runstore.RunStatusRunning,
		},
	}
	b, err := formatSSEEvent(ev)
	if err != nil {
		t.Fatalf("formatSSEEvent: %v", err)
	}
	s := string(b)

	if !strings.HasPrefix(s, "event: run_started\n") {
		t.Errorf("expected 'event: run_started\\n' prefix, got: %q", s)
	}
	if !strings.Contains(s, "data: ") {
		t.Errorf("expected 'data: ' field, got: %q", s)
	}
	if !strings.HasSuffix(s, "\n\n") {
		t.Errorf("expected double-newline suffix, got: %q", s)
	}
	if !strings.Contains(s, "test-run-abc") {
		t.Errorf("expected run_id in payload, got: %q", s)
	}

	// Verify data line is valid JSON.
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var dataLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "data: ") {
			dataLine = strings.TrimPrefix(l, "data: ")
		}
	}
	if dataLine == "" {
		t.Fatal("no data line found")
	}
	var payload SSEEventPayload
	if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
		t.Errorf("data line is not valid JSON: %v", err)
	}
	if payload.RunID != "test-run-abc" {
		t.Errorf("expected run_id 'test-run-abc', got %q", payload.RunID)
	}
}

// ---- handleEvents HTTP handler tests ----

func TestHandleEvents_Headers(t *testing.T) {
	server, _ := setupServerWithRuns(t, nil)

	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(reqCtx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		server.handleAPI(w, req)
	}()

	// Cancel quickly to stop the handler.
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("expected Content-Type text/event-stream, got %q", ct)
	}
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("expected Cache-Control: no-cache, got %q", w.Header().Get("Cache-Control"))
	}
}

func TestHandleEvents_MethodNotAllowed(t *testing.T) {
	server, _ := setupServerWithRuns(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/events", nil)
	w := httptest.NewRecorder()
	server.handleAPI(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleEvents_ClientDisconnect(t *testing.T) {
	server, _ := setupServerWithRuns(t, nil)

	reqCtx, cancel := context.WithCancel(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(reqCtx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		server.handleEvents(w, req)
	}()

	cancel() // simulate client disconnect

	select {
	case <-done:
		// handler returned cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after client disconnect")
	}
}

func TestHandleEvents_ReceivesSSEEvent(t *testing.T) {
	server, _ := setupServerWithRuns(t, nil)

	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(reqCtx)
	w := httptest.NewRecorder()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		server.handleAPI(w, req)
	}()

	// Give the handler time to subscribe before broadcasting.
	time.Sleep(30 * time.Millisecond)

	server.broadcaster.broadcast(sseEvent{
		kind: sseEventRunStarted,
		payload: SSEEventPayload{
			RunID:  "sse-test-run-001",
			Kind:   runstore.RunKindSync,
			Status: runstore.RunStatusRunning,
		},
	})

	// Give the handler time to write the event.
	time.Sleep(30 * time.Millisecond)
	cancel()
	wg.Wait()

	body := w.Body.String()
	if !strings.Contains(body, fmt.Sprintf("event: %s", sseEventRunStarted)) {
		t.Errorf("expected run_started event in body, got: %q", body)
	}
	if !strings.Contains(body, "data: ") {
		t.Errorf("expected data line in body, got: %q", body)
	}
	if !strings.Contains(body, "sse-test-run-001") {
		t.Errorf("expected run_id in body, got: %q", body)
	}
}
