package service

import (
	"github.com/schaermu/quadsyncd/internal/runstore"
	quadsyncd "github.com/schaermu/quadsyncd/internal/sync"
)

// ConflictSummaryFromSync converts a sync.Conflict to a runstore.ConflictSummary.
// It is the single canonical mapping used by all callers.
func ConflictSummaryFromSync(c quadsyncd.Conflict) runstore.ConflictSummary {
	losers := make([]runstore.EffectiveItemSummary, len(c.Losers))
	for i, l := range c.Losers {
		losers[i] = runstore.EffectiveItemSummary{
			MergeKey:   c.MergeKey,
			SourceRepo: l.Repo,
			SourceRef:  l.Ref,
			SourceSHA:  l.SHA,
		}
	}
	return runstore.ConflictSummary{
		MergeKey: c.MergeKey,
		Winner: runstore.EffectiveItemSummary{
			MergeKey:   c.MergeKey,
			SourceRepo: c.WinnerRepo,
			SourceRef:  c.WinnerRef,
			SourceSHA:  c.WinnerSHA,
		},
		Losers: losers,
	}
}
