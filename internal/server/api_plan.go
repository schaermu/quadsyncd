package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/schaermu/quadsyncd/internal/runstore"
	"github.com/schaermu/quadsyncd/internal/server/dto"
)

// planAPIRequest is the optional JSON body accepted by POST /api/plan.
type planAPIRequest struct {
	RepoURL string `json:"repo_url,omitempty"`
	Ref     string `json:"ref,omitempty"`
	Commit  string `json:"commit,omitempty"`
}

// handlePlan handles POST /api/plan (UI-triggered dry-run).
// It is a thin handler: validates and parses the request, delegates execution
// to PlanService.Execute, and writes the response.
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx := r.Context()

	var planReq planAPIRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
	if err := dec.Decode(&planReq); err != nil {
		if !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
	} else {
		// Reject trailing tokens to catch malformed JSON like "{}foo".
		if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, "invalid request body: unexpected trailing data")
			return
		}
	}

	// Reject ref/commit without repo_url — the intent is ambiguous.
	if planReq.RepoURL == "" && (planReq.Ref != "" || planReq.Commit != "") {
		writeJSONError(w, http.StatusBadRequest, "repo_url is required when ref or commit is specified")
		return
	}

	req := runstore.PlanRequest{
		RepoURL: planReq.RepoURL,
		Ref:     planReq.Ref,
		Commit:  planReq.Commit,
	}

	runID, err := s.planSvc.Execute(ctx, req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, dto.PlanTriggerResponse{
			RunID: runID,
			Error: err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, dto.PlanTriggerResponse{
		RunID:  runID,
		Status: "success",
	})
}
