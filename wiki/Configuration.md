# Configuration

quadsyncd is configured via a YAML file, typically located at `~/.config/quadsyncd/config.yaml`.

All string fields support environment variable expansion using `${VAR}` syntax. All paths must resolve to absolute paths after expansion.

## Full Configuration Reference

```yaml
# Git repository configuration
repo:
  # Repository URL (supports SSH or HTTPS)
  url: "git@github.com:ORG/REPO.git"
  # Git ref to track (branch, tag, or commit)
  ref: "refs/heads/main"
  # Subdirectory within the repo containing quadlet files
  subdir: "quadlets"

# Path configuration
paths:
  # Where to sync quadlet files (systemd user quadlet directory)
  # Supports ${HOME} and other environment variable expansion
  quadlet_dir: "${HOME}/.config/containers/systemd"
  # State directory for repo checkout and managed file tracking
  state_dir: "${HOME}/.local/state/quadsyncd"

# Sync behavior
sync:
  # Whether to remove managed quadlet files that no longer exist in the repo
  prune: true
  # Restart policy after sync: "none", "changed", or "all-managed"
  restart: "changed"

# Authentication configuration (choose one)
auth:
  # Path to SSH private key for git operations
  ssh_key_file: "${HOME}/.ssh/quadsyncd_deploy_key"
  # OR: Path to file containing GitHub personal access token for HTTPS
  # https_token_file: "${HOME}/.config/quadsyncd/github_token"

# Webhook server configuration (optional; for `quadsyncd serve` daemon mode)
serve:
  # Enable webhook listener
  enabled: false
  # Listen address (bind to localhost; use reverse proxy for external access)
  listen_addr: "127.0.0.1:8787"
  # Path to file containing GitHub webhook secret for signature verification
  github_webhook_secret_file: "${HOME}/.config/quadsyncd/webhook_secret"
  # Event types to accept from GitHub
  allowed_event_types: ["push"]
  # Git refs to accept (e.g., only trigger on main branch pushes)
  allowed_refs: ["refs/heads/main"]
```

## Section Details

### `repo`

| Field | Required | Description |
|-------|----------|-------------|
| `url` | Yes | Git repository URL. Supports `git@...` (SSH) and `https://...` (HTTPS) schemes. |
| `ref` | Yes | Git reference to track. Examples: `refs/heads/main`, `refs/tags/v1.0`. |
| `subdir` | No | Subdirectory within the repo containing quadlet files. If empty, the repo root is used. |

### `paths`

| Field | Required | Description |
|-------|----------|-------------|
| `quadlet_dir` | Yes | Destination directory for synced quadlet files. Must be an absolute path. Standard Podman rootless location: `~/.config/containers/systemd`. |
| `state_dir` | Yes | Directory for state tracking and repo checkout. Must be an absolute path. |

Key paths derived from `state_dir`:
- **Repo checkout**: `<state_dir>/repo/`
- **State file**: `<state_dir>/state.json`

### `sync`

| Field | Default | Description |
|-------|---------|-------------|
| `prune` | `false` | When `true`, remove managed quadlet files that no longer exist in the repo. Only files previously synced by quadsyncd are removed. |
| `restart` | `changed` | Restart policy after sync. See restart policies below. |

#### Restart Policies

- **`none`**: Only reload systemd daemon (`systemctl --user daemon-reload`), don't restart any units.
- **`changed`**: Reload daemon and restart only units whose quadlet files changed (recommended).
- **`all-managed`**: Reload daemon and restart all units managed by quadsyncd (more disruptive).

### `auth`

Only one authentication method may be configured at a time. The URL scheme must match the auth method.

| Field | Description |
|-------|-------------|
| `ssh_key_file` | Path to SSH private key file. Use with `git@...` or `ssh://...` URLs. |
| `https_token_file` | Path to file containing a GitHub personal access token. Use with `https://...` URLs. |

> **Security**: Never embed tokens or keys directly in the config file. Always use `*_file` fields that reference external files with restrictive permissions (`chmod 600`).

### `serve`

Webhook server configuration for `quadsyncd serve` mode.

| Field | Required | Description |
|-------|----------|-------------|
| `enabled` | No | Set to `true` to enable webhook mode. |
| `listen_addr` | When enabled | Address to bind the HTTP server. Always use `127.0.0.1` (localhost). |
| `github_webhook_secret_file` | When enabled | Path to file containing the GitHub webhook secret for HMAC-SHA256 signature verification. |
| `allowed_event_types` | No | List of GitHub event types to accept. Empty list accepts all events. |
| `allowed_refs` | No | List of Git refs to accept. Empty list accepts all refs. |

## CLI Flags

Global flags available for all commands:

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `~/.config/quadsyncd/config.yaml` | Path to configuration file. |
| `--log-level` | `info` | Log level: `debug`, `info`, `warn`, `error`. |
| `--log-format` | `text` | Log format: `text`, `json`. |

Sync-specific flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `false` | Show what would be done without making changes. |

## Validation

Configuration is validated on load. The following rules are enforced:

- `repo.url` and `repo.ref` are required
- `paths.quadlet_dir` and `paths.state_dir` are required and must be absolute paths
- `sync.restart` must be one of `none`, `changed`, or `all-managed`
- Only one auth method (`ssh_key_file` or `https_token_file`) may be set
- Auth method must match URL scheme (SSH key with SSH URL, HTTPS token with HTTPS URL)
- When `serve.enabled` is `true`, `listen_addr` and `github_webhook_secret_file` are required
