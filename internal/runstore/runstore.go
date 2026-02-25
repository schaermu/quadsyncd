// Package runstore provides a filesystem-backed store for sync run metadata,
// logs, and plan artifacts.
package runstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/schaermu/quadsyncd/internal/multirepo"
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

// Create initializes a new run directory and writes initial meta.json.
func (s *Store) Create(ctx context.Context, meta *RunMeta) error {
	if meta.ID == "" {
		id, err := generateRunID()
		if err != nil {
			return err
		}
		meta.ID = id
	}

	runDir := filepath.Join(s.baseDir, meta.ID)
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
	runDir := filepath.Join(s.baseDir, meta.ID)
	if err := s.writeMetaAtomic(runDir, *meta); err != nil {
		return fmt.Errorf("failed to update meta.json: %w", err)
	}
	return nil
}

// Get retrieves the metadata for a run.
func (s *Store) Get(ctx context.Context, id string) (*RunMeta, error) {
	runDir := filepath.Join(s.baseDir, id)
	metaPath := filepath.Join(runDir, "meta.json")

	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("run not found: %s", id)
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

// AppendLog appends a JSON log line to log.ndjson.
func (s *Store) AppendLog(ctx context.Context, id string, record map[string]interface{}) error {
	runDir := filepath.Join(s.baseDir, id)
	logPath := filepath.Join(runDir, "log.ndjson")

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal log record: %w", err)
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close log file: %w", cerr)
		}
	}()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write log record: %w", err)
	}

	return nil
}

// ReadLog reads all log records from log.ndjson.
// Returns an empty slice if the log file doesn't exist.
func (s *Store) ReadLog(ctx context.Context, id string) ([]map[string]interface{}, error) {
	runDir := filepath.Join(s.baseDir, id)
	logPath := filepath.Join(runDir, "log.ndjson")

	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]interface{}{}, nil
		}
		return nil, fmt.Errorf("failed to read log file: %w", err)
	}

	var records []map[string]interface{}
	for _, line := range strings.Split(string(data), "\n") {
		if len(strings.TrimSpace(line)) == 0 {
			continue
		}
		var record map[string]interface{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			s.logger.Warn("failed to parse log line (skipping)", "id", id, "error", err)
			continue
		}
		records = append(records, record)
	}

	return records, nil
}

// WritePlan writes plan.json and optional artifacts.
func (s *Store) WritePlan(ctx context.Context, id string, plan Plan) error {
	runDir := filepath.Join(s.baseDir, id)
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
	runDir := filepath.Join(s.baseDir, id)
	planPath := filepath.Join(runDir, "plan.json")

	data, err := os.ReadFile(planPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("plan not found for run: %s", id)
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
	runDir := filepath.Join(s.baseDir, id)
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
	runDir := filepath.Join(s.baseDir, id)
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
func (s *Store) Prune(ctx context.Context, maxAge time.Duration) error {
	runs, err := s.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list runs: %w", err)
	}

	cutoff := time.Now().Add(-maxAge)
	var pruned int

	for _, run := range runs {
		if run.StartedAt.Before(cutoff) {
			runDir := filepath.Join(s.baseDir, run.ID)
			if err := os.RemoveAll(runDir); err != nil {
				s.logger.Warn("failed to prune run", "id", run.ID, "error", err)
				continue
			}
			s.logger.Debug("pruned run", "id", run.ID, "started_at", run.StartedAt)
			pruned++
		}
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
