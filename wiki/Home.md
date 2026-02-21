# quadsyncd

**quadsyncd** is a lightweight, secure tool that keeps your Podman container definitions in sync with a Git repository. Each server runs its own agent that pulls updates and reconciles quadlet files, ensuring your containerized services stay up-to-date with version-controlled configurations.

## Key Features

- **Rootless by design**: Runs entirely in userspace without requiring root privileges
- **Git-native**: Tracks quadlet files in any Git repository (GitHub, GitLab, self-hosted)
- **Safe reconciliation**: Computes diffs, applies changes atomically, and tracks managed files
- **Systemd integration**: Timer-based sync with automatic daemon reload and selective unit restarts
- **Flexible authentication**: Supports SSH deploy keys and HTTPS tokens
- **Webhook mode**: Real-time updates via GitHub webhook integration

## Documentation

| Page | Description |
|------|-------------|
| [[Installation]] | Download, install and configure quadsyncd |
| [[Configuration]] | Full configuration reference |
| [[Deployment Guide]] | Deploying with rootless systemd |
| [[Webhook Setup]] | Webhook mode with reverse proxy |
| [[How It Works]] | Architecture and sync engine details |
| [[Troubleshooting]] | Common issues and debugging |
| [[Integration Tests]] | Test strategy and running tests locally |
| [[Release Process]] | Release procedures for maintainers |

## Quick Links

- [GitHub Repository](https://github.com/schaermu/quadsyncd)
- [Latest Release](https://github.com/schaermu/quadsyncd/releases/latest)
- [Contributing Guide](https://github.com/schaermu/quadsyncd/blob/main/CONTRIBUTING.md)
- [Security Policy](https://github.com/schaermu/quadsyncd/blob/main/SECURITY.md)
