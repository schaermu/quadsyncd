package sync

// State tracks the current managed quadlet files
type State struct {
	// Commit is the single-repo commit SHA (legacy; kept for backward compat).
	Commit string `json:"commit,omitempty"`

	// Revisions tracks the last-synced commit SHA per repository URL.
	Revisions map[string]string `json:"revisions,omitempty"`

	ManagedFiles map[string]ManagedFile `json:"managed_files"`
}

// ManagedFile represents a quadlet file under management
type ManagedFile struct {
	SourcePath string `json:"source_path"` // repo-relative path (merge key)
	Hash       string `json:"hash"`        // SHA256 hash of content

	// Provenance (populated for multi-repo; empty for single-repo)
	SourceRepo string `json:"source_repo,omitempty"` // repository URL
	SourceRef  string `json:"source_ref,omitempty"`  // configured ref
	SourceSHA  string `json:"source_sha,omitempty"`  // resolved commit SHA
}

// Plan represents the sync operations to perform
type Plan struct {
	Add    []FileOp
	Update []FileOp
	Delete []FileOp
}

// FileOp represents a file operation
type FileOp struct {
	SourcePath string // absolute path in checkout
	DestPath   string // absolute path in quadlet dir
	Hash       string // content hash

	// Provenance (populated by buildPlanFromEffective; empty in legacy path)
	SourceRepo string
	SourceRef  string
	SourceSHA  string
}
