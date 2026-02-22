# quadsyncd

[![CI](https://img.shields.io/github/actions/workflow/status/schaermu/quadsyncd/ci.yml?branch=main&label=ci)](https://github.com/schaermu/quadsyncd/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/schaermu/quadsyncd/badges/.github/badges/coverage.json)](https://github.com/schaermu/quadsyncd/actions/workflows/ci.yml)
[![E2E Discovery](https://img.shields.io/github/actions/workflow/status/schaermu/quadsyncd/e2e-discovery.yml?branch=main&label=e2e-discovery)](https://github.com/schaermu/quadsyncd/actions/workflows/e2e-discovery.yml)

Automatically synchronize Podman Quadlet files from a Git repository to rootless Podman-enabled servers.

## Overview

**quadsyncd** is a lightweight, secure tool that keeps your Podman container definitions in sync with a Git repository. Each server runs its own agent that pulls updates and reconciles quadlet files, ensuring your containerized services stay up-to-date with version-controlled configurations.

### Key Features

- **Rootless by design**: Runs entirely in userspace without requiring root privileges
- **Git-native**: Tracks quadlet files in any Git repository (GitHub, GitLab, self-hosted)
- **Safe reconciliation**: Computes diffs, applies changes atomically, and tracks managed files
- **Systemd integration**: Timer-based sync with automatic daemon reload and selective unit restarts
- **Flexible authentication**: Supports SSH deploy keys and HTTPS tokens
- **Webhook mode**: Real-time updates via GitHub webhook integration

## Quick Start

```bash
# Install
wget https://github.com/schaermu/quadsyncd/releases/latest/download/quadsyncd_<version>_Linux_x86_64.tar.gz
tar xzf quadsyncd_<version>_Linux_x86_64.tar.gz
mkdir -p ~/.local/bin && cp quadsyncd ~/.local/bin/ && chmod +x ~/.local/bin/quadsyncd

# Configure
mkdir -p ~/.config/quadsyncd
cp config.example.yaml ~/.config/quadsyncd/config.yaml
# Edit config.yaml with your repository details

# Test
quadsyncd sync --dry-run
quadsyncd sync

# Set up timer-based sync
mkdir -p ~/.config/systemd/user
cp packaging/systemd/user/quadsyncd-sync.* ~/.config/systemd/user/
systemctl --user daemon-reload
loginctl enable-linger $USER
systemctl --user enable --now quadsyncd-sync.timer
```

See the [Installation](https://github.com/schaermu/quadsyncd/wiki/Installation) wiki page for detailed instructions.

## Commands

```bash
quadsyncd sync [--dry-run] [--config path]   # One-time sync
quadsyncd serve [--config path]              # Start webhook server
quadsyncd version                            # Show version
```

## Documentation

Full documentation is available on the **[GitHub Wiki](https://github.com/schaermu/quadsyncd/wiki)**:

- [Installation](https://github.com/schaermu/quadsyncd/wiki/Installation) — Download, install and configure
- [Configuration](https://github.com/schaermu/quadsyncd/wiki/Configuration) — Full configuration reference
- [Deployment Guide](https://github.com/schaermu/quadsyncd/wiki/Deployment-Guide) — Deploying with rootless systemd
- [Webhook Setup](https://github.com/schaermu/quadsyncd/wiki/Webhook-Setup) — Webhook mode with reverse proxy
- [How It Works](https://github.com/schaermu/quadsyncd/wiki/How-It-Works) — Architecture and sync engine details
- [Troubleshooting](https://github.com/schaermu/quadsyncd/wiki/Troubleshooting) — Common issues and debugging
- [Integration Tests](https://github.com/schaermu/quadsyncd/wiki/Integration-Tests) — Test strategy and running tests locally
- [Release Process](https://github.com/schaermu/quadsyncd/wiki/Release-Process) — Release procedures for maintainers

## Contributing

We welcome contributions! Please see [`CONTRIBUTING.md`](CONTRIBUTING.md) for development workflow, testing guidelines, and commit conventions.

## Security

To report security issues, see [`SECURITY.md`](SECURITY.md).

## License

MIT License — see [`LICENSE`](LICENSE) for details.

## Roadmap

- [x] Timer-based oneshot sync
- [x] SSH and HTTPS authentication
- [x] Rootless systemd integration
- [x] State tracking and pruning
- [x] Webhook mode with GitHub signature verification
- [ ] Systemd socket activation for webhooks
- [ ] Multi-repo support
- [ ] Dry-run web UI for visualizing changes
