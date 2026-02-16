# quadsyncd

Automatically synchronize Podman Quadlet files from a Git repository to rootless Podman-enabled servers.

## Overview

**quadsyncd** is a lightweight, secure tool that keeps your Podman container definitions in sync with a Git repository. Each server runs its own agent that pulls updates and reconciles quadlet files, ensuring your containerized services stay up-to-date with version-controlled configurations.

### Key Features

- **Rootless by design**: Runs entirely in userspace without requiring root privileges
- **Git-native**: Tracks quadlet files in any Git repository (GitHub, GitLab, self-hosted)
- **Safe reconciliation**: Computes diffs, applies changes atomically, and tracks managed files
- **Systemd integration**: Timer-based sync with automatic daemon reload and selective unit restarts
- **Flexible authentication**: Supports SSH deploy keys and HTTPS tokens
- **Future-ready**: Webhook mode planned for real-time updates

## Quick Start

### Prerequisites

- Podman configured for rootless operation
- Systemd user session
- Git installed
- SSH key or GitHub token for repository access

### Installation

Download the latest release:

```bash
wget https://github.com/schaermu/quadsyncd/releases/latest/download/quadsyncd_<version>_Linux_x86_64.tar.gz
tar xzf quadsyncd_<version>_Linux_x86_64.tar.gz
mkdir -p ~/.local/bin
cp quadsyncd ~/.local/bin/
chmod +x ~/.local/bin/quadsyncd
```

Create configuration:

```bash
mkdir -p ~/.config/quadsyncd
cp config.example.yaml ~/.config/quadsyncd/config.yaml
# Edit config.yaml with your repository details
```

Test manual sync:

```bash
quadsyncd sync --dry-run
quadsyncd sync
```

Set up automatic sync:

```bash
# Install systemd units
mkdir -p ~/.config/systemd/user
cp packaging/systemd/user/quadsyncd-sync.* ~/.config/systemd/user/
systemctl --user daemon-reload

# Enable lingering (so timer runs without active login)
loginctl enable-linger $USER

# Start timer
systemctl --user enable --now quadsyncd-sync.timer
```

## Configuration

Example `~/.config/quadsyncd/config.yaml`:

```yaml
repo:
  url: "git@github.com:your-org/quadlets-repo.git"
  ref: "refs/heads/main"
  subdir: "quadlets"

paths:
  quadlet_dir: "${HOME}/.config/containers/systemd"
  state_dir: "${HOME}/.local/state/quadsyncd"

sync:
  prune: true
  restart: "changed"  # none|changed|all-managed

auth:
  ssh_key_file: "${HOME}/.ssh/quadsyncd_deploy_key"
```

See `config.example.yaml` for all options.

## Deployment

Quadsyncd is designed for rootless Podman servers with systemd user sessions. Key paths:

- **Quadlet files**: `~/.config/containers/systemd/`
- **State tracking**: `~/.local/state/quadsyncd/state.json`
- **Repo checkout**: `~/.local/state/quadsyncd/repo/`

### Timer-Based Sync (Current)

The default mode runs `quadsyncd sync` periodically via systemd timer:

- Fetches repository updates
- Computes diff between repo and local quadlet directory
- Applies changes (add/update/delete files)
- Reloads systemd daemon
- Restarts affected units based on policy

**Frequency**: Configurable via timer (default: every 5 minutes with 30s jitter)

### Webhook Mode (Planned)

Future release will support a long-running `quadsyncd serve` that listens for GitHub webhooks:

- Instant sync on push events
- Signature verification
- Ref filtering (e.g., only sync on main branch)
- Debouncing and single-flight execution

See `docs/webhook-reverse-proxy.md` for planned architecture.

## How It Works

1. **Fetch**: Clone or update Git repository to state directory
2. **Discover**: Scan repo subdir for quadlet files (`.container`, `.volume`, `.network`, `.kube`, etc.)
3. **Plan**: Compute diff against previous sync state:
   - Files to add (new in repo)
   - Files to update (content changed)
   - Files to delete (removed from repo, if prune enabled)
4. **Apply**: Atomically write changes to `~/.config/containers/systemd/`
5. **Track**: Save state with file hashes and git commit
6. **Reload**: Run `systemctl --user daemon-reload` to trigger Podman's quadlet generator
7. **Restart**: Optionally restart units based on policy:
   - `none`: Skip restarts
   - `changed`: Restart only units with modified quadlets
   - `all-managed`: Restart all managed units

## Commands

```bash
# One-time sync
quadsyncd sync [--dry-run] [--config path]

# Future: Start webhook server
quadsyncd serve [--config path]

# Show version
quadsyncd version
```

## Troubleshooting

**Check sync logs:**
```bash
journalctl --user -u quadsyncd-sync.service -n 50
```

**Verify systemd user session:**
```bash
systemctl --user status
```

**Debug authentication:**
```bash
GIT_SSH_COMMAND="ssh -i ~/.ssh/quadsyncd_deploy_key" git ls-remote <repo-url>
```

**Inspect state:**
```bash
cat ~/.local/state/quadsyncd/state.json
```

See `docs/deployment-rootless-systemd.md` for complete troubleshooting guide.

## Contributing

We welcome contributions! Please see:

- [`CONTRIBUTING.md`](CONTRIBUTING.md) - Development workflow, testing guidelines, and commit conventions
- [`AGENTS.md`](AGENTS.md) - Instructions for AI coding agents (different audience)

## Security

- **No secrets in repo**: Use `*_file` config fields for keys and tokens
- **Rootless operation**: No elevated privileges required
- **Atomic writes**: File operations use temp files + rename for safety
- **State tracking**: Only prunes files explicitly managed by quadsyncd

To report security issues, see [`SECURITY.md`](SECURITY.md).

## License

MIT License - see `LICENSE` for details.

## Documentation

- [Deployment Guide](docs/deployment-rootless-systemd.md) - Complete deployment and troubleshooting guide
- [Webhook Setup](docs/webhook-reverse-proxy.md) - Webhook mode architecture (planned feature)
- [Contributing](CONTRIBUTING.md) - Development workflow and guidelines
- [Release Process](docs/releasing.md) - Release procedures for maintainers
- [Security Policy](SECURITY.md) - Security considerations and vulnerability reporting
- [Agent Instructions](AGENTS.md) - Operating instructions for AI coding agents

## Roadmap

- [x] Timer-based oneshot sync
- [x] SSH and HTTPS authentication
- [x] Rootless systemd integration
- [x] State tracking and pruning
- [ ] Webhook mode with GitHub signature verification
- [ ] Systemd socket activation for webhooks
- [ ] Multi-repo support
- [ ] Dry-run web UI for visualizing changes

## Acknowledgments

Built for rootless Podman + systemd environments. Inspired by GitOps principles.
