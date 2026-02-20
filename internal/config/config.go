package config

import (
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

// Config represents the complete quadsyncd configuration
type Config struct {
	Repo  RepoConfig  `yaml:"repo"`
	Paths PathsConfig `yaml:"paths"`
	Sync  SyncConfig  `yaml:"sync"`
	Auth  AuthConfig  `yaml:"auth"`
	Serve ServeConfig `yaml:"serve"`
}

// RepoConfig configures the Git repository source
type RepoConfig struct {
	URL    string `yaml:"url"`
	Ref    string `yaml:"ref"`
	Subdir string `yaml:"subdir"`
}

// PathsConfig configures local filesystem paths
type PathsConfig struct {
	QuadletDir string `yaml:"quadlet_dir"`
	StateDir   string `yaml:"state_dir"`
}

// SyncConfig configures sync behavior
type SyncConfig struct {
	Prune   bool          `yaml:"prune"`
	Restart RestartPolicy `yaml:"restart"`
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
}

// applyDefaults fills in zero-value fields with sensible defaults.
func (c *Config) applyDefaults() {
	if c.Sync.Restart == "" {
		c.Sync.Restart = RestartChanged
	}
}

// Validate checks the configuration for errors
func (c *Config) Validate() error {
	// Validate repo config
	if c.Repo.URL == "" {
		return fmt.Errorf("repo.url is required")
	}
	if c.Repo.Ref == "" {
		return fmt.Errorf("repo.ref is required")
	}

	// Validate paths
	if c.Paths.QuadletDir == "" {
		return fmt.Errorf("paths.quadlet_dir is required")
	}
	if c.Paths.StateDir == "" {
		return fmt.Errorf("paths.state_dir is required")
	}

	// Ensure paths are absolute
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
	default:
		return fmt.Errorf("invalid sync.restart policy: %s (must be none, changed, or all-managed)", c.Sync.Restart)
	}

	// Validate auth: only one auth method may be configured
	if c.Auth.SSHKeyFile != "" && c.Auth.HTTPSTokenFile != "" {
		return fmt.Errorf("auth: only one of ssh_key_file or https_token_file may be set")
	}

	// Validate auth: when auth is configured, the URL scheme must match
	if c.Auth.SSHKeyFile != "" && !c.IsSSH() {
		return fmt.Errorf("auth.ssh_key_file is set but repo.url does not use an SSH scheme (git@ or ssh://)")
	}
	if c.Auth.HTTPSTokenFile != "" && !c.IsHTTPS() {
		return fmt.Errorf("auth.https_token_file is set but repo.url does not use HTTPS scheme")
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

// RepoDir returns the path where the git repository is checked out
func (c *Config) RepoDir() string {
	return filepath.Join(c.Paths.StateDir, "repo")
}

// StateFilePath returns the path to the state tracking file
func (c *Config) StateFilePath() string {
	return filepath.Join(c.Paths.StateDir, "state.json")
}

// QuadletSourceDir returns the path within the repo containing quadlet files
func (c *Config) QuadletSourceDir() string {
	if c.Repo.Subdir == "" {
		return c.RepoDir()
	}
	return filepath.Join(c.RepoDir(), c.Repo.Subdir)
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
