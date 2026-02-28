// Package dto defines Data Transfer Object types for the HTTP API layer.
// These types decouple the API response shapes from domain/storage types.
package dto

// RunResponse is the API representation of a run.
type RunResponse struct {
	ID        string                 `json:"id"`
	Kind      string                 `json:"kind"`
	Trigger   string                 `json:"trigger"`
	StartedAt string                 `json:"started_at"`
	EndedAt   string                 `json:"ended_at,omitempty"`
	Status    string                 `json:"status"`
	DryRun    bool                   `json:"dry_run"`
	Revisions map[string]string      `json:"revisions"`
	Conflicts []ConflictResponse     `json:"conflicts"`
	Summary   map[string]interface{} `json:"summary,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

// RunsListResponse wraps paginated run results.
type RunsListResponse struct {
	Items      []RunResponse `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// PlanResponse is the API representation of a plan.
type PlanResponse struct {
	Requested PlanRequestResponse `json:"requested"`
	Conflicts []ConflictResponse  `json:"conflicts"`
	Ops       []PlanOpResponse    `json:"ops"`
}

// PlanOpResponse is the API representation of a plan operation.
type PlanOpResponse struct {
	Op         string `json:"op"`
	Path       string `json:"path"`
	Unit       string `json:"unit,omitempty"`
	SourceRepo string `json:"source_repo,omitempty"`
	SourceRef  string `json:"source_ref,omitempty"`
	SourceSHA  string `json:"source_sha,omitempty"`
	BeforePath string `json:"before_path,omitempty"`
	AfterPath  string `json:"after_path,omitempty"`
}

// PlanRequestResponse is the API representation of plan request scope.
type PlanRequestResponse struct {
	RepoURL string `json:"repo_url,omitempty"`
	Ref     string `json:"ref,omitempty"`
	Commit  string `json:"commit,omitempty"`
}

// ConflictResponse is the API representation of a conflict.
type ConflictResponse struct {
	MergeKey string                  `json:"merge_key"`
	Winner   EffectiveItemResponse   `json:"winner"`
	Losers   []EffectiveItemResponse `json:"losers"`
}

// EffectiveItemResponse is the API representation of a conflict participant.
type EffectiveItemResponse struct {
	MergeKey   string `json:"merge_key"`
	SourceRepo string `json:"source_repo"`
	SourceRef  string `json:"source_ref"`
	SourceSHA  string `json:"source_sha"`
}

// PlanTriggerResponse is returned when a plan is triggered.
type PlanTriggerResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

// OverviewResponse is the API representation of the dashboard overview.
type OverviewResponse struct {
	Repositories  []OverviewRepo `json:"repositories"`
	LastRunID     string         `json:"last_run_id,omitempty"`
	LastRunStatus string         `json:"last_run_status,omitempty"`
}

// OverviewRepo is the API representation of a tracked repository.
type OverviewRepo struct {
	URL string `json:"url"`
	Ref string `json:"ref,omitempty"`
	SHA string `json:"sha,omitempty"`
}

// ErrorResponse is the standard API error format.
type ErrorResponse struct {
	Error string `json:"error"`
}

// LogEntry is the API representation of a log line.
type LogEntry = map[string]interface{}

// LogsResponse wraps paginated log results.
type LogsResponse struct {
	Items      []LogEntry `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}
