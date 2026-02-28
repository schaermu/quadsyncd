// Package multirepo implements deterministic multi-repository reconciliation.
// It loads quadlet source files from one or more Git checkouts, applies
// path-normalisation and safety checks, then produces an EffectiveState that
// the sync engine can apply atomically.
package multirepo

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schaermu/quadsyncd/internal/config"
	"github.com/schaermu/quadsyncd/internal/git"
	"github.com/schaermu/quadsyncd/internal/quadlet"
)

// RepoFile represents a single file discovered in a repository checkout.
type RepoFile struct {
	// MergeKey is the normalised repo-relative path used for conflict detection.
	MergeKey string
	// AbsPath is the absolute filesystem path in the checkout.
	AbsPath string
}

// RepoState holds the result of loading a single repository.
type RepoState struct {
	Spec   config.RepoSpec
	Commit string
	Files  []RepoFile
}

// EffectiveItem is a file selected for the effective state after merging.
type EffectiveItem struct {
	// MergeKey is the normalised repo-relative path (used as destination filename).
	MergeKey string
	// AbsPath is the absolute path in the winning checkout.
	AbsPath string
	// SourceRepo is the URL of the repository this file came from.
	SourceRepo string
	// SourceRef is the configured ref.
	SourceRef string
	// SourceSHA is the resolved commit SHA.
	SourceSHA string
}

// Conflict records a same-path conflict between two or more repositories.
type Conflict struct {
	MergeKey string
	Winner   EffectiveItem
	Losers   []EffectiveItem
}

// MergeResult is the output of Merge.
type MergeResult struct {
	Items     []EffectiveItem
	Conflicts []Conflict
}

// LoadRepoState checks out a repository and discovers all manageable files in
// it.  It rejects symlinks and path-unsafe entries.
func LoadRepoState(ctx context.Context, spec config.RepoSpec, repoDir, srcDir string, gitClient git.Client) (RepoState, error) {
	commit, err := gitClient.EnsureCheckout(ctx, spec.URL, spec.Ref, repoDir)
	if err != nil {
		return RepoState{}, fmt.Errorf("repo %s: checkout failed: %w", spec.URL, err)
	}

	files, err := loadRepoFiles(srcDir)
	if err != nil {
		return RepoState{}, fmt.Errorf("repo %s: %w", spec.URL, err)
	}

	return RepoState{
		Spec:   spec,
		Commit: commit,
		Files:  files,
	}, nil
}

// loadRepoFiles discovers all non-hidden files under dir, validates them for
// symlinks and path-traversal safety, and returns RepoFiles with a normalised
// MergeKey relative to dir.
func loadRepoFiles(dir string) ([]RepoFile, error) {
	rawFiles, err := quadlet.DiscoverAllFilesWithSymlinkCheck(dir)
	if err != nil {
		return nil, err
	}

	var files []RepoFile
	for _, absPath := range rawFiles {
		rel, err := filepath.Rel(dir, absPath)
		if err != nil {
			return nil, fmt.Errorf("failed to compute relative path for %s: %w", absPath, err)
		}
		mergeKey, err := normalizeMergeKey(rel)
		if err != nil {
			return nil, fmt.Errorf("unsafe path %s: %w", rel, err)
		}
		files = append(files, RepoFile{
			MergeKey: mergeKey,
			AbsPath:  absPath,
		})
	}
	return files, nil
}

// normalizeMergeKey cleans and validates a repo-relative path as a merge key.
// It rejects absolute paths, ".." traversal, and Windows-style drive prefixes.
func normalizeMergeKey(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path not allowed")
	}
	// Reject Windows drive prefixes (defensive)
	if len(rel) >= 2 && rel[1] == ':' {
		return "", fmt.Errorf("windows-style drive prefix not allowed")
	}
	cleaned := filepath.ToSlash(filepath.Clean(rel))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("path traversal not allowed")
	}
	return cleaned, nil
}

// Merge produces a MergeResult from multiple RepoStates.
// States are sorted by priority desc then original-slice-index asc (stable
// tie-break). Same-path conflicts are resolved according to conflictMode.
// Unit-name collisions (different paths → same systemd unit) always fail.
func Merge(states []RepoState, conflictMode config.ConflictMode) (MergeResult, error) {
	// Sort states: higher priority wins; ties resolved by original index (earlier = wins).
	type indexedState struct {
		state RepoState
		idx   int
	}
	indexed := make([]indexedState, len(states))
	for i, s := range states {
		indexed[i] = indexedState{state: s, idx: i}
	}
	sort.SliceStable(indexed, func(a, b int) bool {
		pa := indexed[a].state.Spec.Priority
		pb := indexed[b].state.Spec.Priority
		if pa != pb {
			return pa > pb
		}
		return indexed[a].idx < indexed[b].idx
	})

	// Build per-merge-key candidate lists (in priority order).
	type candidate struct {
		item EffectiveItem
		rank int
	}
	candidates := make(map[string][]candidate)

	for rank, is := range indexed {
		s := is.state
		for _, f := range s.Files {
			item := EffectiveItem{
				MergeKey:   f.MergeKey,
				AbsPath:    f.AbsPath,
				SourceRepo: s.Spec.URL,
				SourceRef:  s.Spec.Ref,
				SourceSHA:  s.Commit,
			}
			candidates[f.MergeKey] = append(candidates[f.MergeKey], candidate{item: item, rank: rank})
		}
	}

	// Process in deterministic key order.
	keys := make([]string, 0, len(candidates))
	for k := range candidates {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var items []EffectiveItem
	var conflicts []Conflict
	var conflictErrors []string

	for _, key := range keys {
		cands := candidates[key]
		if len(cands) == 1 {
			items = append(items, cands[0].item)
			continue
		}

		// Conflict: multiple repos contribute the same merge key.
		winner := cands[0].item
		losers := make([]EffectiveItem, 0, len(cands)-1)
		for _, c := range cands[1:] {
			losers = append(losers, c.item)
		}
		conflicts = append(conflicts, Conflict{MergeKey: key, Winner: winner, Losers: losers})

		switch conflictMode {
		case config.ConflictFail:
			loserDescs := make([]string, len(losers))
			for i, l := range losers {
				loserDescs[i] = fmt.Sprintf("%s@%s", l.SourceRepo, l.SourceRef)
			}
			conflictErrors = append(conflictErrors,
				fmt.Sprintf("  path %q: winner=%s@%s, losers=[%s]",
					key, winner.SourceRepo, winner.SourceRef,
					strings.Join(loserDescs, ", "),
				),
			)
		case config.ConflictPreferHighestPriority:
			items = append(items, winner)
		default:
			return MergeResult{}, fmt.Errorf("unknown conflict_handling mode: %q", conflictMode)
		}
	}

	if conflictMode == config.ConflictFail && len(conflictErrors) > 0 {
		return MergeResult{}, fmt.Errorf(
			"same-path conflicts detected (conflict_handling=fail):\n%s",
			strings.Join(conflictErrors, "\n"),
		)
	}

	// Unit-name collision detection is always strict.
	if err := detectUnitNameCollisions(items); err != nil {
		return MergeResult{}, err
	}

	return MergeResult{Items: items, Conflicts: conflicts}, nil
}

// detectUnitNameCollisions checks whether any two EffectiveItems from different
// source paths would produce the same systemd unit name.
func detectUnitNameCollisions(items []EffectiveItem) error {
	type unitSrc struct {
		mergeKey   string
		sourceRepo string
	}
	unitMap := make(map[string]unitSrc)
	var collisions []string

	for _, item := range items {
		if !quadlet.IsQuadletFile(item.MergeKey) {
			continue
		}
		unitName := quadlet.UnitNameFromQuadlet(item.MergeKey)
		existing, seen := unitMap[unitName]
		if !seen {
			unitMap[unitName] = unitSrc{mergeKey: item.MergeKey, sourceRepo: item.SourceRepo}
			continue
		}
		if existing.mergeKey != item.MergeKey {
			collisions = append(collisions,
				fmt.Sprintf("  unit %q: %s (from %s) vs %s (from %s)",
					unitName,
					existing.mergeKey, existing.sourceRepo,
					item.MergeKey, item.SourceRepo,
				),
			)
		}
	}

	if len(collisions) > 0 {
		return fmt.Errorf(
			"unit-name collisions detected (ensure generated unit names are unique across repos):\n%s",
			strings.Join(collisions, "\n"),
		)
	}
	return nil
}
