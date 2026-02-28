package server

import (
	"fmt"
	"net/http"
	"time"
)

// handleEvents serves GET /api/events as a Server-Sent Events stream.
// It streams run lifecycle events (run_started, run_updated, log_appended, plan_ready)
// by subscribing to the disk-backed Broadcaster.
//
// The write deadline is cleared per-connection via http.ResponseController so the
// server-level WriteTimeout does not terminate long-lived SSE connections.
// Keep-alive comments (": ping") are sent every 15 seconds.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Clear the per-connection write deadline so long-lived SSE connections
	// are not terminated by the server-level WriteTimeout.
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
