package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// RestartPolicy defines when to restart units after sync
type RestartPolicy string

const (
	RestartNone       RestartPolicy = "none"
	RestartChanged    RestartPolicy = "changed"
	RestartAllManaged RestartPolicy = "all-managed"
)

// ConflictMode defines how same-path conflicts across repos are resolved.
type ConflictMode string

const (
	// ConflictPreferHighestPriority chooses the highest-priority repo and warns.
	ConflictPreferHighestPriority ConflictMode = "prefer_highest_priority"
	// ConflictFail returns an error enumerating all conflicts.
	ConflictFail ConflictMode = "fail"
)

// Config represents the complete quadsyncd configuration
type Config struct {
	Repo         RepoConfig  `yaml:"repo"`
	Repositories []RepoSpec  `yaml:"repositories"`
	Paths        PathsConfig `yaml:"paths"`
	Sync         SyncConfig  `yaml:"sync"`
	Auth         AuthConfig  `yaml:"auth"`
	Serve        ServeConfig `yaml:"serve"`
}

// RepoConfig configures the Git repository source (legacy single-repo field)
type RepoConfig struct {
	URL    string `yaml:"url"`
	Ref    string `yaml:"ref"`
	Subdir string `yaml:"subdir"`
}

// RepoSpec describes a single repository entry in the multi-repo list.
type RepoSpec struct {
	URL      string      `yaml:"url"`
	Ref      string      `yaml:"ref"`
	Priority int         `yaml:"priority"`
	Subdir   string      `yaml:"subdir"`
	Auth     *AuthConfig `yaml:"auth,omitempty"`
}

// PathsConfig configures local filesystem paths
type PathsConfig struct {
	QuadletDir string `yaml:"quadlet_dir"`
	StateDir   string `yaml:"state_dir"`
}

// SyncConfig configures sync behavior
type SyncConfig struct {
	Prune            bool          `yaml:"prune"`
	Restart          RestartPolicy `yaml:"restart"`
	ConflictHandling ConflictMode  `yaml:"conflict_handling"`
}

// AuthConfig configures Git authentication
type AuthConfig struct {
	SSHKeyFile     string `yaml:"ssh_key_file"`
	HTTPSTokenFile string `yaml:"https_token_file"`
}

// ServeConfig configures the webhook server (future)
type ServeConfig struct {
	Enabled                 bool     `yaml:"enabled"`
	ListenAddr              string   `yaml:"listen_addr"`
	GitHubWebhookSecretFile string   `yaml:"github_webhook_secret_file"`
	AllowedEventTypes       []string `yaml:"allowed_event_types"`
	AllowedRefs             []string `yaml:"allowed_refs"`
}

// Load reads and parses the configuration file
func Load(path string) (*Config, error) {
	// Expand environment variables in path
	path = os.ExpandEnv(path)

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Expand environment variables in string fields
	cfg.expandEnv()

	// Apply defaults
	cfg.applyDefaults()

	// Validate
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// expandEnv expands environment variables in all string fields
func (c *Config) expandEnv() {
	c.Repo.URL = os.ExpandEnv(c.Repo.URL)
	c.Repo.Ref = os.ExpandEnv(c.Repo.Ref)
	c.Repo.Subdir = os.ExpandEnv(c.Repo.Subdir)
	c.Paths.QuadletDir = os.ExpandEnv(c.Paths.QuadletDir)
	c.Paths.StateDir = os.ExpandEnv(c.Paths.StateDir)
	c.Auth.SSHKeyFile = os.ExpandEnv(c.Auth.SSHKeyFile)
	c.Auth.HTTPSTokenFile = os.ExpandEnv(c.Auth.HTTPSTokenFile)
	c.Serve.ListenAddr = os.ExpandEnv(c.Serve.ListenAddr)
	c.Serve.GitHubWebhookSecretFile = os.ExpandEnv(c.Serve.GitHubWebhookSecretFile)
	for i := range c.Repositories {
		c.Repositories[i].URL = os.ExpandEnv(c.Repositories[i].URL)
		c.Repositories[i].Ref = os.ExpandEnv(c.Repositories[i].Ref)
		c.Repositories[i].Subdir = os.ExpandEnv(c.Repositories[i].Subdir)
		if c.Repositories[i].Auth != nil {
			c.Repositories[i].Auth.SSHKeyFile = os.ExpandEnv(c.Repositories[i].Auth.SSHKeyFile)
			c.Repositories[i].Auth.HTTPSTokenFile = os.ExpandEnv(c.Repositories[i].Auth.HTTPSTokenFile)
		}
	}
}

// applyDefaults fills in zero-value fields with sensible defaults.
func (c *Config) applyDefaults() {
	if c.Sync.Restart == "" {
		c.Sync.Restart = RestartChanged
	}
	if c.Sync.ConflictHandling == "" {
		c.Sync.ConflictHandling = ConflictPreferHighestPriority
	}
}

// Validate checks the configuration for errors
func (c *Config) Validate() error {
	hasLegacyRepo := c.Repo.URL != "" || c.Repo.Ref != ""
	hasRepoList := len(c.Repositories) > 0

	if hasLegacyRepo && hasRepoList {
		return fmt.Errorf("repo and repositories are mutually exclusive; use repositories for multi-repo support")
	}
	if !hasLegacyRepo && !hasRepoList {
		return fmt.Errorf("repo.url is required")
	}

	if hasLegacyRepo {
		if c.Repo.URL == "" {
			return fmt.Errorf("repo.url is required")
		}
		if c.Repo.Ref == "" {
			return fmt.Errorf("repo.ref is required")
		}
		if err := validateAuth(&c.Auth, c.Repo.URL); err != nil {
			return err
		}
	} else {
		for i, spec := range c.Repositories {
			if spec.URL == "" {
				return fmt.Errorf("repositories[%d].url is required", i)
			}
			if spec.Ref == "" {
				return fmt.Errorf("repositories[%d].ref is required", i)
			}
			if spec.Subdir != "" {
				if err := validateSubdir(spec.Subdir, i); err != nil {
					return err
				}
			}
			auth := &c.Auth
			if spec.Auth != nil {
				auth = spec.Auth
			}
			if err := validateAuth(auth, spec.URL); err != nil {
				return fmt.Errorf("repositories[%d] auth: %w", i, err)
			}
		}
	}

	// Validate paths
	if c.Paths.QuadletDir == "" {
		return fmt.Errorf("paths.quadlet_dir is required")
	}
	if c.Paths.StateDir == "" {
		return fmt.Errorf("paths.state_dir is required")
	}
	if !filepath.IsAbs(c.Paths.QuadletDir) {
		return fmt.Errorf("paths.quadlet_dir must be an absolute path: %s", c.Paths.QuadletDir)
	}
	if !filepath.IsAbs(c.Paths.StateDir) {
		return fmt.Errorf("paths.state_dir must be an absolute path: %s", c.Paths.StateDir)
	}

	// Validate restart policy
	switch c.Sync.Restart {
	case RestartNone, RestartChanged, RestartAllManaged:
		// valid
	case "":
		// valid (applyDefaults sets the default; direct struct construction omits it)
	default:
		return fmt.Errorf("invalid sync.restart policy: %s (must be none, changed, or all-managed)", c.Sync.Restart)
	}

	// Validate conflict handling mode
	switch c.Sync.ConflictHandling {
	case ConflictPreferHighestPriority, ConflictFail, "":
		// valid
	default:
		return fmt.Errorf("invalid sync.conflict_handling: %s (must be prefer_highest_priority or fail)", c.Sync.ConflictHandling)
	}

	// Validate serve config if enabled
	if c.Serve.Enabled {
		if c.Serve.ListenAddr == "" {
			return fmt.Errorf("serve.listen_addr is required when serve is enabled")
		}
		if c.Serve.GitHubWebhookSecretFile == "" {
			return fmt.Errorf("serve.github_webhook_secret_file is required when serve is enabled")
		}
	}

	return nil
}

// validateAuth checks that an AuthConfig is consistent with the given repo URL.
func validateAuth(auth *AuthConfig, repoURL string) error {
	if auth.SSHKeyFile != "" && auth.HTTPSTokenFile != "" {
		return fmt.Errorf("auth: only one of ssh_key_file or https_token_file may be set")
	}
	isSSH := strings.HasPrefix(repoURL, "git@") || strings.HasPrefix(repoURL, "ssh://")
	isHTTPS := strings.HasPrefix(repoURL, "https://")
	if auth.SSHKeyFile != "" && !isSSH {
		return fmt.Errorf("auth.ssh_key_file is set but repo.url does not use an SSH scheme (git@ or ssh://)")
	}
	if auth.HTTPSTokenFile != "" && !isHTTPS {
		return fmt.Errorf("auth.https_token_file is set but repo.url does not use HTTPS scheme")
	}
	return nil
}

// validateSubdir checks that a subdir value is repo-relative and traversal-safe.
func validateSubdir(subdir string, idx int) error {
	if filepath.IsAbs(subdir) {
		return fmt.Errorf("repositories[%d].subdir must be a relative path: %s", idx, subdir)
	}
	cleaned := filepath.ToSlash(filepath.Clean(subdir))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("repositories[%d].subdir must not contain path traversal: %s", idx, subdir)
	}
	return nil
}

// RepoID returns a stable, collision-resistant directory-safe identifier for
// the given repository URL, derived from the first 8 bytes of SHA-256.
func RepoID(url string) string {
	h := sha256.Sum256([]byte(url))
	return fmt.Sprintf("%x", h[:8])
}

// RepoDir returns the path where the git repository is checked out (single-repo)
func (c *Config) RepoDir() string {
	return filepath.Join(c.Paths.StateDir, "repo")
}

// StateFilePath returns the path to the state tracking file
func (c *Config) StateFilePath() string {
	return filepath.Join(c.Paths.StateDir, "state.json")
}

// QuadletSourceDir returns the path within the repo containing quadlet files (single-repo)
func (c *Config) QuadletSourceDir() string {
	if c.Repo.Subdir == "" {
		return c.RepoDir()
	}
	return filepath.Join(c.RepoDir(), c.Repo.Subdir)
}

// RepoDirForSpec returns the checkout directory for a RepoSpec under the state root.
func (c *Config) RepoDirForSpec(spec RepoSpec) string {
	return filepath.Join(c.Paths.StateDir, "repos", RepoID(spec.URL))
}

// QuadletSourceDirForSpec returns the quadlet source directory for a RepoSpec.
func (c *Config) QuadletSourceDirForSpec(spec RepoSpec) string {
	repoDir := c.RepoDirForSpec(spec)
	if spec.Subdir == "" {
		return repoDir
	}
	return filepath.Join(repoDir, spec.Subdir)
}

// EffectiveRepositories returns the list of repos to sync.
// If Repositories is set, it is returned as-is; otherwise the legacy Repo
// field is wrapped into a single-element list for uniform handling.
func (c *Config) EffectiveRepositories() []RepoSpec {
	if len(c.Repositories) > 0 {
		return c.Repositories
	}
	return []RepoSpec{{URL: c.Repo.URL, Ref: c.Repo.Ref, Subdir: c.Repo.Subdir}}
}

// AuthForSpec returns the effective AuthConfig for a repo spec.
// A per-spec auth override takes precedence over the global auth.
func (c *Config) AuthForSpec(spec RepoSpec) AuthConfig {
	if spec.Auth != nil {
		return *spec.Auth
	}
	return c.Auth
}

// AuthMethod returns a description of the configured auth method
func (c *Config) AuthMethod() string {
	if c.Auth.SSHKeyFile != "" {
		return "ssh"
	}
	if c.Auth.HTTPSTokenFile != "" {
		return "https"
	}
	return "none"
}

// IsHTTPS returns true if the repo URL uses HTTPS
func (c *Config) IsHTTPS() bool {
	return strings.HasPrefix(c.Repo.URL, "https://")
}

// IsSSH returns true if the repo URL uses SSH
func (c *Config) IsSSH() bool {
	return strings.HasPrefix(c.Repo.URL, "git@") || strings.HasPrefix(c.Repo.URL, "ssh://")
}
