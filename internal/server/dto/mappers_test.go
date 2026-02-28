package dto_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/schaermu/quadsyncd/internal/runstore"
	"github.com/schaermu/quadsyncd/internal/server/dto"
)

// ---- RunResponseFromMeta ----

func TestRunResponseFromMeta_BasicFields(t *testing.T) {
	started := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	ended := time.Date(2026, 1, 15, 10, 0, 5, 0, time.UTC)

	meta := &runstore.RunMeta{
		ID:        "test-id-123",
		Kind:      runstore.RunKindSync,
		Trigger:   runstore.TriggerTimer,
		StartedAt: started,
		EndedAt:   &ended,
		Status:    runstore.RunStatusSuccess,
		DryRun:    false,
		Revisions: map[string]string{"https://github.com/org/repo": "abc123"},
		Conflicts: []runstore.ConflictSummary{},
		Error:     "",
	}

	r := dto.RunResponseFromMeta(meta)

	if r.ID != "test-id-123" {
		t.Errorf("ID: got %q, want %q", r.ID, "test-id-123")
	}
	if r.Kind != "sync" {
		t.Errorf("Kind: got %q, want %q", r.Kind, "sync")
	}
	if r.Trigger != "timer" {
		t.Errorf("Trigger: got %q, want %q", r.Trigger, "timer")
	}
	if r.Status != "success" {
		t.Errorf("Status: got %q, want %q", r.Status, "success")
	}
	if r.DryRun != false {
		t.Error("DryRun: expected false")
	}
	if len(r.Revisions) != 1 {
		t.Errorf("Revisions length: got %d, want 1", len(r.Revisions))
	}
}

func TestRunResponseFromMeta_TimeFormatRFC3339Nano(t *testing.T) {
	// Use a nanosecond-precision time to verify RFC3339Nano is used.
	started := time.Date(2026, 1, 15, 10, 0, 0, 123456789, time.UTC)
	ended := time.Date(2026, 1, 15, 10, 0, 5, 987654321, time.UTC)

	meta := &runstore.RunMeta{
		StartedAt: started,
		EndedAt:   &ended,
		Revisions: map[string]string{},
		Conflicts: []runstore.ConflictSummary{},
	}

	r := dto.RunResponseFromMeta(meta)

	want := started.Format(time.RFC3339Nano)
	if r.StartedAt != want {
		t.Errorf("StartedAt: got %q, want %q", r.StartedAt, want)
	}
	wantEnded := ended.Format(time.RFC3339Nano)
	if r.EndedAt != wantEnded {
		t.Errorf("EndedAt: got %q, want %q", r.EndedAt, wantEnded)
	}
}

func TestRunResponseFromMeta_EndedAtOmittedWhenNil(t *testing.T) {
	meta := &runstore.RunMeta{
		StartedAt: time.Now().UTC(),
		EndedAt:   nil,
		Revisions: map[string]string{},
		Conflicts: []runstore.ConflictSummary{},
	}

	r := dto.RunResponseFromMeta(meta)

	if r.EndedAt != "" {
		t.Errorf("EndedAt: expected empty string when nil, got %q", r.EndedAt)
	}

	// Verify it's omitted in JSON.
	b, _ := json.Marshal(r)
	if json.Valid(b) && containsKey(t, b, "ended_at") {
		t.Error("ended_at should be omitted from JSON when nil")
	}
}

func TestRunResponseFromMeta_ConflictsNeverNull(t *testing.T) {
	// Nil conflicts slice should produce [] not null in JSON.
	meta := &runstore.RunMeta{
		StartedAt: time.Now().UTC(),
		Revisions: map[string]string{},
		Conflicts: nil,
	}

	r := dto.RunResponseFromMeta(meta)

	b, _ := json.Marshal(r)
	if !containsSubstring(b, `"conflicts":[]`) {
		t.Errorf("conflicts should serialize as [] not null; got: %s", string(b))
	}
}

func TestRunResponseFromMeta_ErrorOmittedWhenEmpty(t *testing.T) {
	meta := &runstore.RunMeta{
		StartedAt: time.Now().UTC(),
		Revisions: map[string]string{},
		Conflicts: []runstore.ConflictSummary{},
		Error:     "",
	}

	r := dto.RunResponseFromMeta(meta)

	b, _ := json.Marshal(r)
	if containsKey(t, b, "error") {
		t.Error("error field should be omitted from JSON when empty")
	}
}

// ---- RunsListResponseFromMetas ----

func TestRunsListResponseFromMetas_Items(t *testing.T) {
	runs := []runstore.RunMeta{
		{
			ID:        "run-1",
			Kind:      runstore.RunKindSync,
			Trigger:   runstore.TriggerWebhook,
			StartedAt: time.Now().UTC(),
			Status:    runstore.RunStatusSuccess,
			Revisions: map[string]string{},
			Conflicts: []runstore.ConflictSummary{},
		},
		{
			ID:        "run-2",
			Kind:      runstore.RunKindPlan,
			Trigger:   runstore.TriggerUI,
			StartedAt: time.Now().UTC(),
			Status:    runstore.RunStatusError,
			Revisions: map[string]string{},
			Conflicts: []runstore.ConflictSummary{},
		},
	}

	resp := dto.RunsListResponseFromMetas(runs, "cursor-abc")

	if len(resp.Items) != 2 {
		t.Fatalf("Items length: got %d, want 2", len(resp.Items))
	}
	if resp.Items[0].ID != "run-1" {
		t.Errorf("Items[0].ID: got %q, want %q", resp.Items[0].ID, "run-1")
	}
	if resp.Items[1].ID != "run-2" {
		t.Errorf("Items[1].ID: got %q, want %q", resp.Items[1].ID, "run-2")
	}
	if resp.NextCursor != "cursor-abc" {
		t.Errorf("NextCursor: got %q, want %q", resp.NextCursor, "cursor-abc")
	}
}

func TestRunsListResponseFromMetas_EmptySlice(t *testing.T) {
	resp := dto.RunsListResponseFromMetas([]runstore.RunMeta{}, "")

	if len(resp.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(resp.Items))
	}
	b, _ := json.Marshal(resp)
	if !containsSubstring(b, `"items":[]`) {
		t.Errorf("items should serialize as [] for empty slice; got: %s", string(b))
	}
}

// ---- PlanResponseFromPlan ----

func TestPlanResponseFromPlan_OpsMapping(t *testing.T) {
	plan := &runstore.Plan{
		Requested: runstore.PlanRequest{
			RepoURL: "https://github.com/org/repo",
			Ref:     "refs/heads/main",
			Commit:  "deadbeef",
		},
		Conflicts: []runstore.ConflictSummary{},
		Ops: []runstore.PlanOp{
			{
				Op:         "add",
				Path:       "app.container",
				Unit:       "app.service",
				SourceRepo: "https://github.com/org/repo",
				SourceRef:  "refs/heads/main",
				SourceSHA:  "deadbeef",
				AfterPath:  "0000-after.container",
			},
			{
				Op:         "delete",
				Path:       "old.container",
				Unit:       "old.service",
				BeforePath: "0001-before.container",
			},
		},
	}

	r := dto.PlanResponseFromPlan(plan)

	if r.Requested.RepoURL != "https://github.com/org/repo" {
		t.Errorf("Requested.RepoURL: got %q", r.Requested.RepoURL)
	}
	if r.Requested.Ref != "refs/heads/main" {
		t.Errorf("Requested.Ref: got %q", r.Requested.Ref)
	}
	if r.Requested.Commit != "deadbeef" {
		t.Errorf("Requested.Commit: got %q", r.Requested.Commit)
	}

	if len(r.Ops) != 2 {
		t.Fatalf("Ops length: got %d, want 2", len(r.Ops))
	}

	add := r.Ops[0]
	if add.Op != "add" || add.Path != "app.container" || add.Unit != "app.service" {
		t.Errorf("add op fields wrong: %+v", add)
	}
	if add.SourceSHA != "deadbeef" {
		t.Errorf("SourceSHA: got %q, want %q", add.SourceSHA, "deadbeef")
	}
	if add.AfterPath != "0000-after.container" {
		t.Errorf("AfterPath: got %q", add.AfterPath)
	}
	if add.BeforePath != "" {
		t.Errorf("BeforePath should be empty for add op, got %q", add.BeforePath)
	}

	del := r.Ops[1]
	if del.Op != "delete" || del.BeforePath != "0001-before.container" {
		t.Errorf("delete op fields wrong: %+v", del)
	}
}

func TestPlanResponseFromPlan_ConflictsNeverNull(t *testing.T) {
	plan := &runstore.Plan{
		Conflicts: nil,
		Ops:       nil,
	}

	r := dto.PlanResponseFromPlan(plan)

	b, _ := json.Marshal(r)
	if !containsSubstring(b, `"conflicts":[]`) {
		t.Errorf("conflicts should serialize as [] not null; got: %s", string(b))
	}
	if !containsSubstring(b, `"ops":[]`) {
		t.Errorf("ops should serialize as [] not null; got: %s", string(b))
	}
}

// ---- ConflictResponseFromSummary ----

func TestConflictResponseFromSummary(t *testing.T) {
	c := runstore.ConflictSummary{
		MergeKey: "app.container",
		Winner: runstore.EffectiveItemSummary{
			MergeKey:   "app.container",
			SourceRepo: "https://github.com/org/repo1",
			SourceRef:  "refs/heads/main",
			SourceSHA:  "winner-sha",
		},
		Losers: []runstore.EffectiveItemSummary{
			{
				MergeKey:   "app.container",
				SourceRepo: "https://github.com/org/repo2",
				SourceRef:  "refs/heads/main",
				SourceSHA:  "loser-sha",
			},
		},
	}

	r := dto.ConflictResponseFromSummary(c)

	if r.MergeKey != "app.container" {
		t.Errorf("MergeKey: got %q", r.MergeKey)
	}
	if r.Winner.SourceSHA != "winner-sha" {
		t.Errorf("Winner.SourceSHA: got %q", r.Winner.SourceSHA)
	}
	if r.Winner.SourceRepo != "https://github.com/org/repo1" {
		t.Errorf("Winner.SourceRepo: got %q", r.Winner.SourceRepo)
	}
	if len(r.Losers) != 1 {
		t.Fatalf("Losers length: got %d, want 1", len(r.Losers))
	}
	if r.Losers[0].SourceSHA != "loser-sha" {
		t.Errorf("Losers[0].SourceSHA: got %q", r.Losers[0].SourceSHA)
	}
}

func TestConflictResponseFromSummary_LosersNeverNull(t *testing.T) {
	c := runstore.ConflictSummary{
		MergeKey: "x.container",
		Losers:   nil,
	}

	r := dto.ConflictResponseFromSummary(c)

	b, _ := json.Marshal(r)
	if !containsSubstring(b, `"losers":[]`) {
		t.Errorf("losers should serialize as [] not null; got: %s", string(b))
	}
}

// ---- helpers ----

func containsSubstring(b []byte, sub string) bool {
	return bytes.Contains(b, []byte(sub))
}

// containsKey checks whether the JSON bytes contain the given key at any nesting level.
func containsKey(t *testing.T, b []byte, key string) bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}
