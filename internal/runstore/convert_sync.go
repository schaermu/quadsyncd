package runstore

import quadsyncd "github.com/schaermu/quadsyncd/internal/sync"

// ConflictSummaryFromSync converts a sync.Conflict to a ConflictSummary.
// It is the canonical mapping used by all callers (serve mode, CLI sync, plan).
func ConflictSummaryFromSync(c quadsyncd.Conflict) ConflictSummary {
	losers := make([]EffectiveItemSummary, len(c.Losers))
	for i, l := range c.Losers {
		losers[i] = EffectiveItemSummary{
			MergeKey:   c.MergeKey,
			SourceRepo: l.Repo,
			SourceRef:  l.Ref,
			SourceSHA:  l.SHA,
		}
	}
	return ConflictSummary{
		MergeKey: c.MergeKey,
		Winner: EffectiveItemSummary{
			MergeKey:   c.MergeKey,
			SourceRepo: c.WinnerRepo,
			SourceRef:  c.WinnerRef,
			SourceSHA:  c.WinnerSHA,
		},
		Losers: losers,
	}
}
