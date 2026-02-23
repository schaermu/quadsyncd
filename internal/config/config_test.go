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
				Sync: SyncConfig{
					Restart: RestartChanged,
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
				Sync: SyncConfig{
					Restart: RestartChanged,
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
				Sync: SyncConfig{
					Restart: RestartChanged,
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
				Sync: SyncConfig{
					Restart: RestartChanged,
				},
			},
			wantErr: true,
		},
		{
			name: "missing quadlet_dir",
			cfg: Config{
				Repo: RepoConfig{
					URL: "git@github.com:test/repo.git",
					Ref: "main",
				},
				Paths: PathsConfig{
					StateDir: "/absolute/state",
				},
				Auth: AuthConfig{
					SSHKeyFile: "/key",
				},
				Sync: SyncConfig{
					Restart: RestartChanged,
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
				Sync: SyncConfig{
					Restart: RestartChanged,
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
				Sync: SyncConfig{
					Restart: RestartChanged,
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

func TestLoad_NonExistentFile(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "bad.yaml")
	if err := os.WriteFile(path, []byte("not: [valid: yaml: {{"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestLoad_ValidationFailure(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "invalid.yaml")
	// Valid YAML but fails validation (missing required fields)
	content := "repo:\n  url: \"\"\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid config, got nil")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Multi-repo config tests
// ──────────────────────────────────────────────────────────────────────────────

func TestLoad_MultiRepo(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "multi.yaml")
	content := `
repositories:
  - url: "git@github.com:org/repo1.git"
    ref: "refs/heads/main"
    priority: 10
    subdir: "quadlets"
  - url: "https://github.com/org/repo2.git"
    ref: "refs/heads/stable"
    priority: 5
    auth:
      https_token_file: "/token"

paths:
  quadlet_dir: "/absolute/quadlets"
  state_dir: "/absolute/state"

sync:
  restart: "changed"
  conflict_handling: "prefer_highest_priority"

auth:
  ssh_key_file: "/key"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load multi-repo config: %v", err)
	}
	if len(cfg.Repositories) != 2 {
		t.Fatalf("expected 2 repositories, got %d", len(cfg.Repositories))
	}
	if cfg.Repositories[0].URL != "git@github.com:org/repo1.git" {
		t.Errorf("repo[0].url = %q", cfg.Repositories[0].URL)
	}
	if cfg.Repositories[0].Priority != 10 {
		t.Errorf("repo[0].priority = %d, want 10", cfg.Repositories[0].Priority)
	}
	if cfg.Sync.ConflictHandling != ConflictPreferHighestPriority {
		t.Errorf("conflict_handling = %q, want prefer_highest_priority", cfg.Sync.ConflictHandling)
	}
}

func TestValidate_MultiRepo(t *testing.T) {
	validPaths := PathsConfig{QuadletDir: "/q", StateDir: "/s"}
	validSync := SyncConfig{Restart: RestartChanged}

	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid multi-repo",
			cfg: Config{
				Repositories: []RepoSpec{
					{URL: "git@github.com:org/r1.git", Ref: "main"},
					{URL: "https://github.com/org/r2.git", Ref: "main"},
				},
				Paths: validPaths,
				Sync:  validSync,
			},
			wantErr: false,
		},
		{
			name: "repo and repositories mutually exclusive",
			cfg: Config{
				Repo:         RepoConfig{URL: "git@github.com:org/r.git", Ref: "main"},
				Repositories: []RepoSpec{{URL: "git@github.com:org/r2.git", Ref: "main"}},
				Paths:        validPaths,
				Sync:         validSync,
			},
			wantErr: true,
		},
		{
			name: "missing url in repositories entry",
			cfg: Config{
				Repositories: []RepoSpec{{Ref: "main"}},
				Paths:        validPaths,
				Sync:         validSync,
			},
			wantErr: true,
		},
		{
			name: "missing ref in repositories entry",
			cfg: Config{
				Repositories: []RepoSpec{{URL: "git@github.com:org/r.git"}},
				Paths:        validPaths,
				Sync:         validSync,
			},
			wantErr: true,
		},
		{
			name: "subdir traversal rejected",
			cfg: Config{
				Repositories: []RepoSpec{{URL: "git@github.com:org/r.git", Ref: "main", Subdir: "../etc"}},
				Paths:        validPaths,
				Sync:         validSync,
			},
			wantErr: true,
		},
		{
			name: "absolute subdir rejected",
			cfg: Config{
				Repositories: []RepoSpec{{URL: "git@github.com:org/r.git", Ref: "main", Subdir: "/absolute"}},
				Paths:        validPaths,
				Sync:         validSync,
			},
			wantErr: true,
		},
		{
			name: "per-repo auth both methods rejected",
			cfg: Config{
				Repositories: []RepoSpec{{
					URL: "git@github.com:org/r.git",
					Ref: "main",
					Auth: &AuthConfig{
						SSHKeyFile:     "/key",
						HTTPSTokenFile: "/token",
					},
				}},
				Paths: validPaths,
				Sync:  validSync,
			},
			wantErr: true,
		},
		{
			name: "per-repo ssh key with https url rejected",
			cfg: Config{
				Repositories: []RepoSpec{{
					URL:  "https://github.com/org/r.git",
					Ref:  "main",
					Auth: &AuthConfig{SSHKeyFile: "/key"},
				}},
				Paths: validPaths,
				Sync:  validSync,
			},
			wantErr: true,
		},
		{
			name: "invalid conflict_handling",
			cfg: Config{
				Repositories: []RepoSpec{{URL: "git@github.com:org/r.git", Ref: "main"}},
				Paths:        validPaths,
				Sync:         SyncConfig{Restart: RestartChanged, ConflictHandling: "invalid"},
			},
			wantErr: true,
		},
		{
			name: "valid conflict_handling fail",
			cfg: Config{
				Repositories: []RepoSpec{{URL: "git@github.com:org/r.git", Ref: "main"}},
				Paths:        validPaths,
				Sync:         SyncConfig{Restart: RestartChanged, ConflictHandling: ConflictFail},
			},
			wantErr: false,
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

func TestEffectiveRepositories_LegacyRepo(t *testing.T) {
	cfg := Config{
		Repo: RepoConfig{URL: "git@github.com:org/r.git", Ref: "main", Subdir: "q"},
	}
	repos := cfg.EffectiveRepositories()
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].URL != cfg.Repo.URL {
		t.Errorf("URL = %q, want %q", repos[0].URL, cfg.Repo.URL)
	}
	if repos[0].Subdir != cfg.Repo.Subdir {
		t.Errorf("Subdir = %q, want %q", repos[0].Subdir, cfg.Repo.Subdir)
	}
}

func TestEffectiveRepositories_MultiRepo(t *testing.T) {
	repos := []RepoSpec{
		{URL: "git@github.com:org/r1.git", Ref: "main", Priority: 10},
		{URL: "https://github.com/org/r2.git", Ref: "main", Priority: 5},
	}
	cfg := Config{Repositories: repos}
	got := cfg.EffectiveRepositories()
	if len(got) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(got))
	}
	if got[0].URL != repos[0].URL {
		t.Errorf("repo[0].URL = %q, want %q", got[0].URL, repos[0].URL)
	}
}

func TestRepoDirForSpec(t *testing.T) {
	cfg := Config{Paths: PathsConfig{StateDir: "/state"}}
	spec := RepoSpec{URL: "git@github.com:org/repo.git"}
	got := cfg.RepoDirForSpec(spec)
	// Should be under /state/repos/<id>
	if got == "" {
		t.Fatal("RepoDirForSpec returned empty string")
	}
	if filepath.Dir(got) != filepath.Join("/state", "repos") {
		t.Errorf("RepoDirForSpec() parent = %q, want /state/repos", filepath.Dir(got))
	}
	// Same URL → same ID (stable)
	got2 := cfg.RepoDirForSpec(spec)
	if got != got2 {
		t.Errorf("RepoDirForSpec not stable: %q vs %q", got, got2)
	}
	// Different URL → different ID (collision resistant)
	spec2 := RepoSpec{URL: "git@github.com:org/other.git"}
	got3 := cfg.RepoDirForSpec(spec2)
	if got == got3 {
		t.Errorf("different URLs produced same RepoDirForSpec: %q", got)
	}
}

func TestAuthForSpec(t *testing.T) {
	globalAuth := AuthConfig{SSHKeyFile: "/global-key"}
	perRepoAuth := AuthConfig{HTTPSTokenFile: "/repo-token"}
	cfg := Config{Auth: globalAuth}

	// No per-repo override → global auth
	spec1 := RepoSpec{URL: "git@github.com:org/r.git"}
	got := cfg.AuthForSpec(spec1)
	if got.SSHKeyFile != globalAuth.SSHKeyFile {
		t.Errorf("AuthForSpec without override: got %+v, want %+v", got, globalAuth)
	}

	// Per-repo override → override wins
	spec2 := RepoSpec{URL: "https://github.com/org/r.git", Auth: &perRepoAuth}
	got2 := cfg.AuthForSpec(spec2)
	if got2.HTTPSTokenFile != perRepoAuth.HTTPSTokenFile {
		t.Errorf("AuthForSpec with override: got %+v, want %+v", got2, perRepoAuth)
	}
}

func TestRepoID_Stable(t *testing.T) {
	url := "git@github.com:org/repo.git"
	id1 := RepoID(url)
	id2 := RepoID(url)
	if id1 != id2 {
		t.Errorf("RepoID not stable: %q vs %q", id1, id2)
	}
	if id1 == "" {
		t.Error("RepoID returned empty string")
	}
	// Different URLs → different IDs
	other := RepoID("git@github.com:org/other.git")
	if id1 == other {
		t.Errorf("different URLs produced same RepoID: %q", id1)
	}
}

func TestApplyDefaults_ConflictHandling(t *testing.T) {
	cfg := Config{}
	cfg.applyDefaults()
	if cfg.Sync.ConflictHandling != ConflictPreferHighestPriority {
		t.Errorf("applyDefaults() conflict_handling = %q, want %q",
			cfg.Sync.ConflictHandling, ConflictPreferHighestPriority)
	}
	// Explicit value must not be overwritten
	cfg2 := Config{Sync: SyncConfig{ConflictHandling: ConflictFail}}
	cfg2.applyDefaults()
	if cfg2.Sync.ConflictHandling != ConflictFail {
		t.Errorf("applyDefaults() overwrote explicit conflict_handling")
	}
}
