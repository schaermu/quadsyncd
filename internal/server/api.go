package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/schaermu/quadsyncd/internal/quadlet"
	"github.com/schaermu/quadsyncd/internal/runstore"
	"github.com/schaermu/quadsyncd/internal/server/dto"
	quadsyncd "github.com/schaermu/quadsyncd/internal/sync"
)

// OverviewRepo is an alias for the DTO type, kept for backward compatibility.
type OverviewRepo = dto.OverviewRepo

// OverviewResponse is an alias for the DTO type, kept for backward compatibility.
type OverviewResponse = dto.OverviewResponse

// RunsListResponse is an alias for the DTO type, kept for backward compatibility.
type RunsListResponse = dto.RunsListResponse

// RunLogsResponse is an alias for the DTO type, kept for backward compatibility.
type RunLogsResponse = dto.LogsResponse

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

// TimerInfo is the response shape for GET /api/timer.
type TimerInfo struct {
	Unit   string `json:"unit"`
	Active bool   `json:"active"`
}

// handleAPI routes /api/ requests to the appropriate handler.
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

// handleOverview serves GET /api/overview.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	repos := s.cfg.EffectiveRepositories()

	state, err := loadSyncState(s.cfg.StateFilePath())
	if err != nil {
		s.logger.Warn("failed to load sync state for overview", "error", err)
	}

	overviewRepos := make([]dto.OverviewRepo, len(repos))
	for i, spec := range repos {
		or := dto.OverviewRepo{URL: spec.URL, Ref: spec.Ref}
		if state.Revisions != nil {
			if sha, ok := state.Revisions[spec.URL]; ok {
				or.SHA = sha
			}
		}
		if or.SHA == "" && state.Commit != "" && len(repos) == 1 {
			or.SHA = state.Commit
		}
		overviewRepos[i] = or
	}

	resp := dto.OverviewResponse{Repositories: overviewRepos}

	if runs, err := s.store.List(ctx); err == nil && len(runs) > 0 {
		resp.LastRunID = runs[0].ID
		resp.LastRunStatus = string(runs[0].Status)
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleRuns serves GET /api/runs?limit=&cursor=.
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
	writeJSON(w, http.StatusOK, dto.RunsListResponseFromMetas(items, nextCursor))
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

	writeJSON(w, http.StatusOK, dto.RunResponseFromMeta(meta))
}

// handleRunLogs serves GET /api/runs/{id}/logs?level=&component=&q=&since=&limit=&cursor=.
func (s *Server) handleRunLogs(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

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

	writeJSON(w, http.StatusOK, dto.PlanResponseFromPlan(plan))
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

// handleTimer serves GET /api/timer.
func (s *Server) handleTimer(w http.ResponseWriter, r *http.Request) {
	const timerUnit = "quadsyncd-sync.timer"
	info := TimerInfo{Unit: timerUnit}

	ctxTimeout, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	status, _ := s.systemd.GetUnitStatus(ctxTimeout, timerUnit)
	info.Active = status == "active"

	writeJSON(w, http.StatusOK, info)
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
