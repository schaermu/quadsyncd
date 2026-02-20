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
