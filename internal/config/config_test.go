package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	// Create a temporary config file
	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(tmpfile.Name())
	}()

	content := `
repo:
  url: "git@github.com:test/repo.git"
  ref: "refs/heads/main"
  subdir: "quadlets"

paths:
  quadlet_dir: "/home/user/.config/containers/systemd"
  state_dir: "/home/user/.local/state/quadsyncd"

sync:
  prune: true
  restart: "changed"

auth:
  ssh_key_file: "/home/user/.ssh/key"

serve:
  enabled: false
`

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(tmpfile.Name())
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify loaded values
	if cfg.Repo.URL != "git@github.com:test/repo.git" {
		t.Errorf("expected URL git@github.com:test/repo.git, got %s", cfg.Repo.URL)
	}
	if cfg.Sync.Restart != RestartChanged {
		t.Errorf("expected restart policy changed, got %s", cfg.Sync.Restart)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				Repo: RepoConfig{
					URL: "git@github.com:test/repo.git",
					Ref: "main",
				},
				Paths: PathsConfig{
					QuadletDir: "/absolute/path",
					StateDir:   "/absolute/state",
				},
				Auth: AuthConfig{
					SSHKeyFile: "/key",
				},
				Sync: SyncConfig{
					Restart: RestartChanged,
				},
			},
			wantErr: false,
		},
		{
			name: "missing repo URL",
			cfg: Config{
				Repo: RepoConfig{
					Ref: "main",
				},
				Paths: PathsConfig{
					QuadletDir: "/absolute/path",
					StateDir:   "/absolute/state",
				},
				Auth: AuthConfig{
					SSHKeyFile: "/key",
				},
			},
			wantErr: true,
		},
		{
			name: "relative path",
			cfg: Config{
				Repo: RepoConfig{
					URL: "git@github.com:test/repo.git",
					Ref: "main",
				},
				Paths: PathsConfig{
					QuadletDir: "relative/path",
					StateDir:   "/absolute/state",
				},
				Auth: AuthConfig{
					SSHKeyFile: "/key",
				},
			},
			wantErr: true,
		},
		{
			name: "no auth method is valid for public repos",
			cfg: Config{
				Repo: RepoConfig{
					URL: "https://github.com/test/repo.git",
					Ref: "main",
				},
				Paths: PathsConfig{
					QuadletDir: "/absolute/path",
					StateDir:   "/absolute/state",
				},
				Sync: SyncConfig{
					Restart: RestartChanged,
				},
			},
			wantErr: false,
		},
		{
			name: "both ssh key and https token set",
			cfg: Config{
				Repo: RepoConfig{
					URL: "git@github.com:test/repo.git",
					Ref: "main",
				},
				Paths: PathsConfig{
					QuadletDir: "/absolute/path",
					StateDir:   "/absolute/state",
				},
				Auth: AuthConfig{
					SSHKeyFile:     "/key",
					HTTPSTokenFile: "/token",
				},
			},
			wantErr: true,
		},
		{
			name: "ssh key with https url",
			cfg: Config{
				Repo: RepoConfig{
					URL: "https://github.com/test/repo.git",
					Ref: "main",
				},
				Paths: PathsConfig{
					QuadletDir: "/absolute/path",
					StateDir:   "/absolute/state",
				},
				Auth: AuthConfig{
					SSHKeyFile: "/key",
				},
			},
			wantErr: true,
		},
		{
			name: "https token with ssh url",
			cfg: Config{
				Repo: RepoConfig{
					URL: "git@github.com:test/repo.git",
					Ref: "main",
				},
				Paths: PathsConfig{
					QuadletDir: "/absolute/path",
					StateDir:   "/absolute/state",
				},
				Auth: AuthConfig{
					HTTPSTokenFile: "/token",
				},
			},
			wantErr: true,
		},
		{
			name: "missing repo ref",
			cfg: Config{
				Repo: RepoConfig{
					URL: "git@github.com:test/repo.git",
				},
				Paths: PathsConfig{
					QuadletDir: "/absolute/path",
					StateDir:   "/absolute/state",
				},
				Auth: AuthConfig{
					SSHKeyFile: "/key",
				},
			},
			wantErr: true,
		},
		{
			name: "missing state_dir",
			cfg: Config{
				Repo: RepoConfig{
					URL: "git@github.com:test/repo.git",
					Ref: "main",
				},
				Paths: PathsConfig{
					QuadletDir: "/absolute/path",
				},
				Auth: AuthConfig{
					SSHKeyFile: "/key",
				},
			},
			wantErr: true,
		},
		{
			name: "relative state_dir",
			cfg: Config{
				Repo: RepoConfig{
					URL: "git@github.com:test/repo.git",
					Ref: "main",
				},
				Paths: PathsConfig{
					QuadletDir: "/absolute/path",
					StateDir:   "relative/state",
				},
				Auth: AuthConfig{
					SSHKeyFile: "/key",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid restart policy",
			cfg: Config{
				Repo: RepoConfig{
					URL: "git@github.com:test/repo.git",
					Ref: "main",
				},
				Paths: PathsConfig{
					QuadletDir: "/absolute/path",
					StateDir:   "/absolute/state",
				},
				Auth: AuthConfig{
					SSHKeyFile: "/key",
				},
				Sync: SyncConfig{
					Restart: "bogus",
				},
			},
			wantErr: true,
		},
		{
			name: "serve enabled missing listen_addr",
			cfg: Config{
				Repo: RepoConfig{
					URL: "git@github.com:test/repo.git",
					Ref: "main",
				},
				Paths: PathsConfig{
					QuadletDir: "/absolute/path",
					StateDir:   "/absolute/state",
				},
				Auth: AuthConfig{
					SSHKeyFile: "/key",
				},
				Sync: SyncConfig{
					Restart: RestartChanged,
				},
				Serve: ServeConfig{
					Enabled:                 true,
					GitHubWebhookSecretFile: "/secret",
				},
			},
			wantErr: true,
		},
		{
			name: "serve enabled missing webhook secret file",
			cfg: Config{
				Repo: RepoConfig{
					URL: "git@github.com:test/repo.git",
					Ref: "main",
				},
				Paths: PathsConfig{
					QuadletDir: "/absolute/path",
					StateDir:   "/absolute/state",
				},
				Auth: AuthConfig{
					SSHKeyFile: "/key",
				},
				Sync: SyncConfig{
					Restart: RestartChanged,
				},
				Serve: ServeConfig{
					Enabled:    true,
					ListenAddr: "127.0.0.1:8080",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigHelpers(t *testing.T) {
	cfg := Config{
		Paths: PathsConfig{
			StateDir: "/home/user/.local/state/quadsyncd",
		},
		Repo: RepoConfig{
			Subdir: "quadlets",
		},
	}

	if got := cfg.RepoDir(); got != filepath.Join(cfg.Paths.StateDir, "repo") {
		t.Errorf("RepoDir() = %s, want %s", got, filepath.Join(cfg.Paths.StateDir, "repo"))
	}

	if got := cfg.StateFilePath(); got != filepath.Join(cfg.Paths.StateDir, "state.json") {
		t.Errorf("StateFilePath() = %s, want %s", got, filepath.Join(cfg.Paths.StateDir, "state.json"))
	}
}

func TestApplyDefaults(t *testing.T) {
	cfg := Config{}
	cfg.applyDefaults()

	if cfg.Sync.Restart != RestartChanged {
		t.Errorf("applyDefaults() did not set restart policy, got %q, want %q", cfg.Sync.Restart, RestartChanged)
	}

	// Explicit value must not be overwritten
	cfg2 := Config{Sync: SyncConfig{Restart: RestartNone}}
	cfg2.applyDefaults()

	if cfg2.Sync.Restart != RestartNone {
		t.Errorf("applyDefaults() overwrote explicit restart policy, got %q, want %q", cfg2.Sync.Restart, RestartNone)
	}
}

func TestQuadletSourceDir(t *testing.T) {
	tests := []struct {
		name   string
		subdir string
		want   string
	}{
		{
			name:   "empty subdir returns RepoDir",
			subdir: "",
			want:   "/state/repo",
		},
		{
			name:   "subdir set returns RepoDir/subdir",
			subdir: "quadlets",
			want:   "/state/repo/quadlets",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				Paths: PathsConfig{StateDir: "/state"},
				Repo:  RepoConfig{Subdir: tt.subdir},
			}
			if got := cfg.QuadletSourceDir(); got != tt.want {
				t.Errorf("QuadletSourceDir() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestAuthMethod(t *testing.T) {
	tests := []struct {
		name string
		auth AuthConfig
		want string
	}{
		{
			name: "ssh key set",
			auth: AuthConfig{SSHKeyFile: "/key"},
			want: "ssh",
		},
		{
			name: "https token set",
			auth: AuthConfig{HTTPSTokenFile: "/token"},
			want: "https",
		},
		{
			name: "no auth",
			auth: AuthConfig{},
			want: "none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Auth: tt.auth}
			if got := cfg.AuthMethod(); got != tt.want {
				t.Errorf("AuthMethod() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestIsHTTPS(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{
			name: "https url",
			url:  "https://github.com/test/repo.git",
			want: true,
		},
		{
			name: "ssh url",
			url:  "ssh://git@github.com/test/repo.git",
			want: false,
		},
		{
			name: "git@ url",
			url:  "git@github.com:test/repo.git",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Repo: RepoConfig{URL: tt.url}}
			if got := cfg.IsHTTPS(); got != tt.want {
				t.Errorf("IsHTTPS() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsSSH(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{
			name: "git@ url",
			url:  "git@github.com:test/repo.git",
			want: true,
		},
		{
			name: "ssh:// url",
			url:  "ssh://git@github.com/test/repo.git",
			want: true,
		},
		{
			name: "https url",
			url:  "https://github.com/test/repo.git",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Repo: RepoConfig{URL: tt.url}}
			if got := cfg.IsSSH(); got != tt.want {
				t.Errorf("IsSSH() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("QUADSYNCD_TEST_HOME", "/home/testuser")

	cfg := Config{
		Repo: RepoConfig{
			URL:    "https://github.com/${QUADSYNCD_TEST_HOME}/repo.git",
			Ref:    "${QUADSYNCD_TEST_HOME}",
			Subdir: "${QUADSYNCD_TEST_HOME}/sub",
		},
		Paths: PathsConfig{
			QuadletDir: "${QUADSYNCD_TEST_HOME}/.config/containers/systemd",
			StateDir:   "${QUADSYNCD_TEST_HOME}/.local/state/quadsyncd",
		},
		Auth: AuthConfig{
			SSHKeyFile:     "${QUADSYNCD_TEST_HOME}/.ssh/key",
			HTTPSTokenFile: "${QUADSYNCD_TEST_HOME}/token",
		},
		Serve: ServeConfig{
			ListenAddr:              "${QUADSYNCD_TEST_HOME}:8080",
			GitHubWebhookSecretFile: "${QUADSYNCD_TEST_HOME}/secret",
		},
	}

	cfg.expandEnv()

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"Repo.URL", cfg.Repo.URL, "https://github.com//home/testuser/repo.git"},
		{"Repo.Ref", cfg.Repo.Ref, "/home/testuser"},
		{"Repo.Subdir", cfg.Repo.Subdir, "/home/testuser/sub"},
		{"Paths.QuadletDir", cfg.Paths.QuadletDir, "/home/testuser/.config/containers/systemd"},
		{"Paths.StateDir", cfg.Paths.StateDir, "/home/testuser/.local/state/quadsyncd"},
		{"Auth.SSHKeyFile", cfg.Auth.SSHKeyFile, "/home/testuser/.ssh/key"},
		{"Auth.HTTPSTokenFile", cfg.Auth.HTTPSTokenFile, "/home/testuser/token"},
		{"Serve.ListenAddr", cfg.Serve.ListenAddr, "/home/testuser:8080"},
		{"Serve.GitHubWebhookSecretFile", cfg.Serve.GitHubWebhookSecretFile, "/home/testuser/secret"},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("expandEnv() %s = %s, want %s", c.name, c.got, c.want)
		}
	}
}
