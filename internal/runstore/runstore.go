// Package runstore provides a filesystem-backed store for sync run metadata,
// logs, and plan artifacts.
package runstore

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/schaermu/quadsyncd/internal/multirepo"
)

// Sentinel errors for run and plan lookups.
var (
	// ErrRunNotFound is returned when a run ID does not exist or is invalid.
	ErrRunNotFound = errors.New("run not found")
	// ErrPlanNotFound is returned when no plan.json exists for a run.
	ErrPlanNotFound = errors.New("plan not found")
)

// RunKind identifies the type of run.
type RunKind string

const (
	// RunKindSync represents a sync operation.
	RunKindSync RunKind = "sync"
	// RunKindPlan represents a plan operation.
	RunKindPlan RunKind = "plan"
)

// RunStatus represents the current status of a run.
type RunStatus string

const (
	// RunStatusRunning indicates the run is in progress.
	RunStatusRunning RunStatus = "running"
	// RunStatusSuccess indicates the run completed successfully.
	RunStatusSuccess RunStatus = "success"
	// RunStatusError indicates the run failed.
	RunStatusError RunStatus = "error"
)

// TriggerSource identifies what initiated a run.
type TriggerSource string

const (
	// TriggerTimer indicates a systemd timer triggered the run.
	TriggerTimer TriggerSource = "timer"
	// TriggerCLI indicates manual CLI execution.
	TriggerCLI TriggerSource = "cli"
	// TriggerWebhook indicates a GitHub webhook triggered the run.
	TriggerWebhook TriggerSource = "webhook"
	// TriggerStartup indicates the daemon startup triggered the run.
	TriggerStartup TriggerSource = "startup"
	// TriggerUI indicates the web UI triggered the run.
	TriggerUI TriggerSource = "ui"
)

// RunMeta holds metadata about a sync run.
type RunMeta struct {
	ID        string                 `json:"id"`
	Kind      RunKind                `json:"kind"`
	Trigger   TriggerSource          `json:"trigger"`
	StartedAt time.Time              `json:"started_at"`
	EndedAt   *time.Time             `json:"ended_at,omitempty"`
	Status    RunStatus              `json:"status"`
	DryRun    bool                   `json:"dry_run"`
	Revisions map[string]string      `json:"revisions"`         // repo_url -> commit_sha
	Conflicts []ConflictSummary      `json:"conflicts"`         // serialized conflicts
	Summary   map[string]interface{} `json:"summary,omitempty"` // counts, best-effort
	Error     string                 `json:"error,omitempty"`
}

// ConflictSummary is the serialized form of multirepo.Conflict.
type ConflictSummary struct {
	MergeKey string                 `json:"merge_key"`
	Winner   EffectiveItemSummary   `json:"winner"`
	Losers   []EffectiveItemSummary `json:"losers"`
}

// EffectiveItemSummary is the serialized form of multirepo.EffectiveItem.
type EffectiveItemSummary struct {
	MergeKey   string `json:"merge_key"`
	SourceRepo string `json:"source_repo"`
	SourceRef  string `json:"source_ref"`
	SourceSHA  string `json:"source_sha"`
}

// PlanRequest describes the scope of a plan operation.
type PlanRequest struct {
	RepoURL string `json:"repo_url,omitempty"` // empty => all repos
	Ref     string `json:"ref,omitempty"`
	Commit  string `json:"commit,omitempty"`
}

// PlanOp describes a single planned file operation.
type PlanOp struct {
	Op         string `json:"op"` // "add", "update", "delete"
	Path       string `json:"path"`
	Unit       string `json:"unit,omitempty"`
	SourceRepo string `json:"source_repo,omitempty"`
	SourceRef  string `json:"source_ref,omitempty"`
	SourceSHA  string `json:"source_sha,omitempty"`
	BeforePath string `json:"before_path,omitempty"` // relative to artifacts/
	AfterPath  string `json:"after_path,omitempty"`  // relative to artifacts/
}

// Plan holds the serialized plan for a run.
type Plan struct {
	Requested PlanRequest       `json:"requested"`
	Conflicts []ConflictSummary `json:"conflicts"`
	Ops       []PlanOp          `json:"ops"`
}

// Store manages run storage on the filesystem.
type Store struct {
	baseDir string
	logger  *slog.Logger
}

// NewStore creates a new Store rooted at baseDir/runs/.
func NewStore(baseDir string, logger *slog.Logger) *Store {
	return &Store{
		baseDir: filepath.Join(baseDir, "runs"),
		logger:  logger,
	}
}

// generateRunID creates a sortable, filesystem-safe run ID.
// Format: YYYYMMDD-HHMMSS-<6-char-hex>
func generateRunID() (string, error) {
	now := time.Now().UTC()
	suffix := make([]byte, 3) // 3 bytes = 6 hex chars
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("failed to generate random suffix: %w", err)
	}
	return fmt.Sprintf("%s-%s",
		now.Format("20060102-150405"),
		hex.EncodeToString(suffix),
	), nil
}

// safeRunDir validates the run ID and returns the safe run directory path.
// Rejects path traversal sequences, absolute paths, and empty IDs.
func (s *Store) safeRunDir(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("%w: ID cannot be empty", ErrRunNotFound)
	}
	// filepath.Base returns "." for empty, "/" for root, and strips any path separators.
	// If the result differs from the input, the ID contains path separators or traversal.
	if filepath.Base(id) != id {
		return "", fmt.Errorf("%w: invalid run ID (path traversal detected): %s", ErrRunNotFound, id)
	}
	return filepath.Join(s.baseDir, id), nil
}

// validateArtifactName validates that an artifact name is a single path element
// without separators or traversal sequences.
func validateArtifactName(name string) error {
	if name == "" {
		return fmt.Errorf("artifact name cannot be empty")
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("invalid artifact name (path traversal detected): %s", name)
	}
	return nil
}

// parseRunIDTimestamp attempts to parse the timestamp from a run ID.
// Run IDs follow the format: YYYYMMDD-HHMMSS-<hex>.
// Returns the parsed time or an error if parsing fails.
func parseRunIDTimestamp(id string) (time.Time, error) {
	// Expected format: 20060102-150405-abcdef
	parts := strings.Split(id, "-")
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("invalid run ID format: %s", id)
	}

	// Combine date and time parts: YYYYMMDD + HHMMSS
	timestampStr := parts[0] + parts[1]
	t, err := time.Parse("20060102150405", timestampStr)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse timestamp from run ID %s: %w", id, err)
	}

	return t, nil
}

// Create initializes a new run directory and writes initial meta.json.
func (s *Store) Create(ctx context.Context, meta *RunMeta) error {
	if meta.ID == "" {
		id, err := generateRunID()
		if err != nil {
			return err
		}
		meta.ID = id
	}

	runDir, err := s.safeRunDir(meta.ID)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(runDir, 0755); err != nil {
		return fmt.Errorf("failed to create run directory: %w", err)
	}

	if err := s.writeMetaAtomic(runDir, *meta); err != nil {
		return fmt.Errorf("failed to write meta.json: %w", err)
	}

	s.logger.Debug("created run", "id", meta.ID, "kind", meta.Kind, "trigger", meta.Trigger)
	return nil
}

// Update writes the current state of meta to meta.json (atomic).
func (s *Store) Update(ctx context.Context, meta *RunMeta) error {
	runDir, err := s.safeRunDir(meta.ID)
	if err != nil {
		return err
	}

	// Check that the run exists before updating
	metaPath := filepath.Join(runDir, "meta.json")
	if _, err := os.Stat(metaPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrRunNotFound, meta.ID)
		}
		return fmt.Errorf("failed to stat meta.json: %w", err)
	}

	if err := s.writeMetaAtomic(runDir, *meta); err != nil {
		return fmt.Errorf("failed to update meta.json: %w", err)
	}
	return nil
}

// Get retrieves the metadata for a run.
func (s *Store) Get(ctx context.Context, id string) (*RunMeta, error) {
	runDir, err := s.safeRunDir(id)
	if err != nil {
		return nil, err
	}

	metaPath := filepath.Join(runDir, "meta.json")

	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrRunNotFound, id)
		}
		return nil, fmt.Errorf("failed to read meta.json: %w", err)
	}

	var meta RunMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse meta.json: %w", err)
	}

	return &meta, nil
}

// List returns all runs sorted newest first.
func (s *Store) List(ctx context.Context) ([]RunMeta, error) {
	if err := os.MkdirAll(s.baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to ensure runs directory exists: %w", err)
	}

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read runs directory: %w", err)
	}

	var runs []RunMeta
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := s.Get(ctx, entry.Name())
		if err != nil {
			s.logger.Warn("failed to load run metadata (skipping)", "id", entry.Name(), "error", err)
			continue
		}
		runs = append(runs, *meta)
	}

	// Sort newest first
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].StartedAt.After(runs[j].StartedAt)
	})

	return runs, nil
}

// AppendLog appends a pre-encoded JSON line to log.ndjson.
func (s *Store) AppendLog(ctx context.Context, id string, line []byte) (err error) {
	runDir, err := s.safeRunDir(id)
	if err != nil {
		return err
	}

	logPath := filepath.Join(runDir, "log.ndjson")

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close log file: %w", cerr)
		}
	}()

	// Append line with newline
	if _, err = f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("failed to write log record: %w", err)
	}

	return nil
}

// ReadLog reads all log records from log.ndjson.
// Returns an empty slice if the log file doesn't exist.
func (s *Store) ReadLog(ctx context.Context, id string) ([]map[string]interface{}, error) {
	runDir, err := s.safeRunDir(id)
	if err != nil {
		return nil, err
	}

	logPath := filepath.Join(runDir, "log.ndjson")

	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]interface{}{}, nil
		}
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			s.logger.Warn("failed to close log file", "path", logPath, "error", cerr)
		}
	}()

	var records []map[string]interface{}
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var record map[string]interface{}
		if err := json.Unmarshal(line, &record); err != nil {
			s.logger.Warn("failed to parse log line (skipping)", "id", id, "error", err)
			continue
		}
		records = append(records, record)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan log file: %w", err)
	}

	return records, nil
}

// WritePlan writes plan.json and optional artifacts.
func (s *Store) WritePlan(ctx context.Context, id string, plan Plan) error {
	runDir, err := s.safeRunDir(id)
	if err != nil {
		return err
	}

	planPath := filepath.Join(runDir, "plan.json")

	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal plan: %w", err)
	}

	if err := os.WriteFile(planPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write plan.json: %w", err)
	}

	return nil
}

// ReadPlan reads plan.json for a run.
// Returns an error if the plan file doesn't exist.
func (s *Store) ReadPlan(ctx context.Context, id string) (*Plan, error) {
	runDir, err := s.safeRunDir(id)
	if err != nil {
		return nil, err
	}

	planPath := filepath.Join(runDir, "plan.json")

	data, err := os.ReadFile(planPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w for run: %s", ErrPlanNotFound, id)
		}
		return nil, fmt.Errorf("failed to read plan.json: %w", err)
	}

	var plan Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("failed to parse plan.json: %w", err)
	}

	return &plan, nil
}

// WriteArtifact writes a plan artifact (before/after quadlet content).
func (s *Store) WriteArtifact(ctx context.Context, id, name string, content []byte) error {
	if err := validateArtifactName(name); err != nil {
		return err
	}

	runDir, err := s.safeRunDir(id)
	if err != nil {
		return err
	}

	artifactsDir := filepath.Join(runDir, "artifacts")

	if err := os.MkdirAll(artifactsDir, 0755); err != nil {
		return fmt.Errorf("failed to create artifacts directory: %w", err)
	}

	artifactPath := filepath.Join(artifactsDir, name)
	if err := os.WriteFile(artifactPath, content, 0644); err != nil {
		return fmt.Errorf("failed to write artifact: %w", err)
	}

	return nil
}

// ReadArtifact reads a plan artifact.
func (s *Store) ReadArtifact(ctx context.Context, id, name string) ([]byte, error) {
	if err := validateArtifactName(name); err != nil {
		return nil, err
	}

	runDir, err := s.safeRunDir(id)
	if err != nil {
		return nil, err
	}

	artifactPath := filepath.Join(runDir, "artifacts", name)

	data, err := os.ReadFile(artifactPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("artifact not found: %s", name)
		}
		return nil, fmt.Errorf("failed to read artifact: %w", err)
	}

	return data, nil
}

// Prune removes runs older than the given duration.
// Uses a three-level fallback for determining run age:
// 1. meta.json StartedAt (if readable)
// 2. Timestamp parsed from run ID (YYYYMMDD-HHMMSS format)
// 3. Directory modification time
func (s *Store) Prune(ctx context.Context, maxAge time.Duration) error {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Nothing to prune
		}
		return fmt.Errorf("failed to read runstore base dir: %w", err)
	}

	cutoff := time.Now().Add(-maxAge)
	var pruned int

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		runID := entry.Name()
		runDir := filepath.Join(s.baseDir, runID)

		// Try to get run time from meta.json first
		var refTime time.Time
		meta, err := s.Get(ctx, runID)
		if err == nil {
			refTime = meta.StartedAt
		} else {
			// Fallback 1: Parse timestamp from run ID
			if t, err := parseRunIDTimestamp(runID); err == nil {
				refTime = t
			} else {
				// Fallback 2: Use directory modification time
				info, err := entry.Info()
				if err != nil {
					s.logger.Warn("failed to stat run directory while pruning", "id", runID, "error", err)
					continue
				}
				refTime = info.ModTime()
			}
		}

		// Only prune if older than cutoff
		if !refTime.Before(cutoff) {
			continue
		}

		if err := os.RemoveAll(runDir); err != nil {
			s.logger.Warn("failed to prune run", "id", runID, "error", err)
			continue
		}

		s.logger.Debug("pruned run", "id", runID, "ref_time", refTime)
		pruned++
	}

	if pruned > 0 {
		s.logger.Info("pruned old runs", "count", pruned, "max_age", maxAge)
	}

	return nil
}

// writeMetaAtomic writes meta.json using temp file + rename.
func (s *Store) writeMetaAtomic(runDir string, meta RunMeta) error {
	metaPath := filepath.Join(runDir, "meta.json")

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal meta: %w", err)
	}

	tmp, err := os.CreateTemp(runDir, ".meta.json.*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if rerr := os.Remove(tmpPath); rerr != nil && !os.IsNotExist(rerr) {
			s.logger.Warn("failed to remove temp file", "path", tmpPath, "error", rerr)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, metaPath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// ConflictSummaryFromMultirepo converts a multirepo.Conflict to ConflictSummary.
func ConflictSummaryFromMultirepo(c multirepo.Conflict) ConflictSummary {
	losers := make([]EffectiveItemSummary, len(c.Losers))
	for i, l := range c.Losers {
		losers[i] = EffectiveItemSummary{
			MergeKey:   l.MergeKey,
			SourceRepo: l.SourceRepo,
			SourceRef:  l.SourceRef,
			SourceSHA:  l.SourceSHA,
		}
	}
	return ConflictSummary{
		MergeKey: c.MergeKey,
		Winner: EffectiveItemSummary{
			MergeKey:   c.Winner.MergeKey,
			SourceRepo: c.Winner.SourceRepo,
			SourceRef:  c.Winner.SourceRef,
			SourceSHA:  c.Winner.SourceSHA,
		},
		Losers: losers,
	}
}
