# How It Works

## Sync Engine

quadsyncd's sync engine performs the following steps on each run:

1. **Fetch**: Clone or update the Git repository to the state directory (`<state_dir>/repo/`)
2. **Discover**: Scan the repository subdirectory for all files, including quadlet files and companion files (e.g. environment files, config files). Hidden files and directories are skipped.
3. **Plan**: Compute a diff against the previous sync state:
   - Files to **add** (new in repo)
   - Files to **update** (content changed since last sync)
   - Files to **delete** (removed from repo, if prune is enabled)
4. **Apply**: Atomically write changes to the quadlet directory (`~/.config/containers/systemd/`) using temp file + rename
5. **Track**: Save state with file hashes and the current git commit to `<state_dir>/state.json`
6. **Reload**: Run `systemctl --user daemon-reload` to trigger Podman's quadlet generator
7. **Restart**: Optionally restart units based on the configured restart policy

## Supported Quadlet Extensions

The following Podman Quadlet file extensions are recognized:

- `.container` — Container definitions
- `.volume` — Volume definitions
- `.network` — Network definitions
- `.kube` — Kubernetes YAML definitions
- `.image` — Image build definitions
- `.build` — Build definitions
- `.pod` — Pod definitions

All quadlet files are mapped to systemd `.service` units by Podman's generator (e.g. `myapp.container` → `myapp.service`).

## Companion Files

In addition to quadlet files, quadsyncd syncs all non-hidden files from the repository subdirectory. This allows you to include companion files alongside your quadlets, such as:

- Environment files (`.env`)
- Configuration files
- Secret references

## State Tracking

quadsyncd maintains a state file (`state.json`) that records:

- The git commit hash of the last successful sync
- A map of managed file paths to their content hashes

This state is used to:
- Detect which files have changed since the last sync
- Determine which files to prune (only files quadsyncd previously wrote)
- Avoid unnecessary restarts when nothing has changed

## Restart Policies

After applying changes, quadsyncd reloads the systemd daemon and optionally restarts units:

| Policy | Behavior |
|--------|----------|
| `none` | Only `systemctl --user daemon-reload` — no unit restarts |
| `changed` | Reload + `systemctl --user try-restart` for units whose quadlet files changed |
| `all-managed` | Reload + `systemctl --user try-restart` for all managed units |

The `try-restart` command only restarts units that are currently running, avoiding errors for stopped units.

## Webhook Mode

When running as `quadsyncd serve`, the server:

1. Performs an initial sync on startup
2. Listens for GitHub webhook POST requests on the configured address
3. Verifies the HMAC-SHA256 signature (`X-Hub-Signature-256`) before processing
4. Filters events by type (`allowed_event_types`) and ref (`allowed_refs`)
5. Debounces rapid webhook events (2-second delay)
6. Executes syncs with single-flight semantics (at most one sync runs at a time; one additional run is queued if events arrive during a sync)

## Authentication

quadsyncd supports two authentication methods for git operations:

### SSH

When `auth.ssh_key_file` is configured, quadsyncd sets the `GIT_SSH_COMMAND` environment variable to use the specified key for all git operations.

### HTTPS

When `auth.https_token_file` is configured, quadsyncd reads the token from the file and injects it via git's credential helper mechanism using an environment variable (`QUADSYNCD_GIT_TOKEN`).

Only one auth method may be configured at a time.
