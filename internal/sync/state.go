package sync

// State tracks the current managed quadlet files
type State struct {
	Commit       string                 `json:"commit"`
	ManagedFiles map[string]ManagedFile `json:"managed_files"`
}

// ManagedFile represents a quadlet file under management
type ManagedFile struct {
	SourcePath string `json:"source_path"` // relative path within repo
	Hash       string `json:"hash"`        // SHA256 hash of content
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
}
