package dto

import (
	"time"

	"github.com/schaermu/quadsyncd/internal/runstore"
)

// RunResponseFromMeta converts a RunMeta domain object to a RunResponse DTO.
// Time fields are formatted as RFC 3339 strings, matching time.Time.MarshalJSON output.
func RunResponseFromMeta(m *runstore.RunMeta) RunResponse {
	r := RunResponse{
		ID:        m.ID,
		Kind:      string(m.Kind),
		Trigger:   string(m.Trigger),
		StartedAt: m.StartedAt.Format(time.RFC3339Nano),
		Status:    string(m.Status),
		DryRun:    m.DryRun,
		Revisions: m.Revisions,
		Summary:   m.Summary,
		Error:     m.Error,
	}
	if m.EndedAt != nil {
		r.EndedAt = m.EndedAt.Format(time.RFC3339Nano)
	}
	r.Conflicts = make([]ConflictResponse, len(m.Conflicts))
	for i, c := range m.Conflicts {
		r.Conflicts[i] = ConflictResponseFromSummary(c)
	}
	return r
}

// RunsListResponseFromMetas converts a slice of RunMeta and a pagination cursor
// into a RunsListResponse DTO.
func RunsListResponseFromMetas(runs []runstore.RunMeta, nextCursor string) RunsListResponse {
	items := make([]RunResponse, len(runs))
	for i := range runs {
		items[i] = RunResponseFromMeta(&runs[i])
	}
	return RunsListResponse{Items: items, NextCursor: nextCursor}
}

// PlanResponseFromPlan converts a Plan domain object to a PlanResponse DTO.
func PlanResponseFromPlan(p *runstore.Plan) PlanResponse {
	ops := make([]PlanOpResponse, len(p.Ops))
	for i, op := range p.Ops {
		ops[i] = PlanOpResponse{
			Op:         op.Op,
			Path:       op.Path,
			Unit:       op.Unit,
			SourceRepo: op.SourceRepo,
			SourceRef:  op.SourceRef,
			SourceSHA:  op.SourceSHA,
			BeforePath: op.BeforePath,
			AfterPath:  op.AfterPath,
		}
	}
	conflicts := make([]ConflictResponse, len(p.Conflicts))
	for i, c := range p.Conflicts {
		conflicts[i] = ConflictResponseFromSummary(c)
	}
	return PlanResponse{
		Requested: PlanRequestResponse{
			RepoURL: p.Requested.RepoURL,
			Ref:     p.Requested.Ref,
			Commit:  p.Requested.Commit,
		},
		Conflicts: conflicts,
		Ops:       ops,
	}
}

// ConflictResponseFromSummary converts a ConflictSummary to a ConflictResponse DTO.
func ConflictResponseFromSummary(c runstore.ConflictSummary) ConflictResponse {
	losers := make([]EffectiveItemResponse, len(c.Losers))
	for i, l := range c.Losers {
		losers[i] = EffectiveItemResponse{
			MergeKey:   l.MergeKey,
			SourceRepo: l.SourceRepo,
			SourceRef:  l.SourceRef,
			SourceSHA:  l.SourceSHA,
		}
	}
	return ConflictResponse{
		MergeKey: c.MergeKey,
		Winner: EffectiveItemResponse{
			MergeKey:   c.Winner.MergeKey,
			SourceRepo: c.Winner.SourceRepo,
			SourceRef:  c.Winner.SourceRef,
			SourceSHA:  c.Winner.SourceSHA,
		},
		Losers: losers,
	}
}
