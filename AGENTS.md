# AGENTS.md

This document provides guidance for AI coding agents and human contributors working on quadsyncd.

## Project Overview

**quadsyncd** is a Go-based CLI tool that automatically synchronizes Podman Quadlet files from a GitHub repository to a rootless Podman-enabled server.

**Deployment Model**: Each target server runs its own quadsyncd agent that pulls from GitHub (not a controller that pushes over SSH).

**Modes**:
- **Oneshot sync** (current): Triggered by systemd timer, fetches repo, reconciles quadlet files, reloads systemd
- **Webhook daemon** (planned): Long-running HTTP server that responds to GitHub webhooks

## Repository Structure

```
.
├── cmd/quadsyncd/          # CLI entrypoint (Cobra-based)
├── internal/
│   ├── config/             # YAML config loading & validation
│   ├── git/                # Git operations (shells out to git command)
│   ├── quadlet/            # Quadlet file discovery & classification
│   ├── sync/               # Reconciliation engine (plan/apply)
│   └── systemduser/        # Systemd --user operations
├── packaging/systemd/user/ # Systemd user units (service, timer, webhook)
├── docs/                   # Deployment guides
├── .github/workflows/      # CI (test, lint, vuln) + release (goreleaser)
└── config.example.yaml     # Example configuration
```

## Development Workflow

### Local Commands

```bash
make fmt      # Format code with gofmt
make test     # Run tests with race detector
make lint     # Run golangci-lint
make vuln     # Check for vulnerabilities with govulncheck
make build    # Build binary
make install  # Install to ~/.local/bin and copy systemd units
```

### Running Locally

```bash
# Create a test config
cp config.example.yaml config.yaml
# Edit config.yaml with test repo details

# Dry-run sync
go run ./cmd/quadsyncd sync --config config.yaml --dry-run

# Actual sync (requires git, systemctl --user to be available)
go run ./cmd/quadsyncd sync --config config.yaml
```

### Testing Against Rootless Podman

The sync engine is designed to work with rootless Podman + systemd user units. For local testing:

1. Ensure systemd user session is active (`systemctl --user status`)
2. Quadlet directory: `~/.config/containers/systemd/`
3. State directory: `~/.local/state/quadsyncd/`
4. After sync, Podman's generator creates units: `systemctl --user list-units | grep container`

## Code Conventions

### Architecture Principles

1. **Testability**: Keep sync logic pure. Isolate side effects (git, filesystem, systemctl) behind interfaces.
2. **No network calls in unit tests**: Use temp directories and mock implementations.
3. **Explicit error handling**: Return errors with context; avoid panics.

### Interfaces for Testing

Key interfaces:
- `git.Client`: Clone/fetch/checkout operations
- `systemduser.Systemd`: daemon-reload, unit restarts
- Future: filesystem abstraction for plan/apply

See `internal/sync/sync_test.go` for examples of mocks.

### Logging

Use `log/slog` (stdlib) with structured logging:

```go
logger.Info("syncing files", "source", src, "dest", dst)
logger.Error("sync failed", "error", err)
```

Log levels: debug, info, warn, error (configurable via `--log-level`).

### Configuration

- Format: YAML
- Package: `internal/config`
- Environment variable expansion: Use `${HOME}`, `${VAR}` in config values
- Validation: All paths must be absolute after expansion

## Security Rules

### No Secrets in Code or Repo

- Never commit secrets (SSH keys, tokens, webhook secrets)
- Config supports `*_file` fields for secrets:
  - `auth.ssh_key_file`
  - `auth.https_token_file`
  - `serve.github_webhook_secret_file`

### Webhook Signature Verification (Future)

When implementing `quadsyncd serve`:
- Verify `X-Hub-Signature-256` HMAC using configured secret
- Reject unsigned or invalid requests
- Validate event type and allowed refs

## Systemd Integration (Rootless)

### Paths

- Quadlet files: `~/.config/containers/systemd/` (standard Podman rootless location)
- State tracking: `~/.local/state/quadsyncd/state.json`
- Repo checkout: `~/.local/state/quadsyncd/repo/`

### User Units

- `quadsyncd-sync.service`: Type=oneshot, runs `quadsyncd sync`
- `quadsyncd-sync.timer`: Periodic trigger (default: every 5m, with 30s jitter)
- `quadsyncd-webhook.service`: Type=simple (future webhook mode)

### Lingering Requirement

For timers to run without active login session:
```bash
loginctl enable-linger $USER
```

Must be documented in deployment guides.

### Systemd Operations

- Always use `systemctl --user` (not root)
- After file changes: `systemctl --user daemon-reload` (triggers Podman generator)
- Restart policy:
  - `none`: Only reload
  - `changed`: `try-restart` units with changed quadlets
  - `all-managed`: `try-restart` all managed units

## CI/CD Pipeline

### Pull Request / Push to Main (`.github/workflows/ci.yml`)

1. Go version from `go.mod` baseline (1.26.x)
2. `go mod tidy` check (must not produce diff)
3. `go test ./...` with race detector
4. `govulncheck ./...`
5. `golangci-lint run`
6. Build on linux/amd64, linux/arm64, darwin/arm64

### Release (`.github/workflows/release.yml`)

Triggered on `v*` tags:
1. GoReleaser builds cross-platform binaries
2. Archives include: binary, LICENSE, README.md, config.example.yaml, systemd units
3. Generates checksums
4. Creates GitHub Release with artifacts

## Release Process

1. Ensure main branch is stable (CI passing)
2. Tag release:
   ```bash
   git tag -a v0.1.0 -m "Initial release"
   git push origin v0.1.0
   ```
3. GitHub Actions automatically builds and publishes release
4. Announce in README/docs if needed

## Future: Webhook Daemon (`serve` command)

When implementing webhook mode:

### Design

- HTTP listener bound to `127.0.0.1` (localhost only)
- Reverse proxy (Caddy/Nginx) or tunnel (cloudflared) forwards public traffic
- Verify GitHub webhook signatures (HMAC with secret)
- Debounce + single-flight sync execution (avoid parallel runs)
- Reuse same reconciliation engine as `sync` command

### Systemd Integration

- Use `quadsyncd-webhook.service` (already in packaging/)
- Consider socket activation (`.socket` unit) for on-demand start

### Configuration

Already defined in `config.yaml`:
```yaml
serve:
  enabled: true
  listen_addr: "127.0.0.1:8787"
  github_webhook_secret_file: "${HOME}/.config/quadsyncd/webhook_secret"
  allowed_event_types: ["push"]
  allowed_refs: ["refs/heads/main"]
```

## Common Tasks

### Adding a New Config Field

1. Update `internal/config/config.go` struct
2. Add validation in `Config.Validate()`
3. Update `config.example.yaml`
4. Add test case in `internal/config/config_test.go`

### Adding a New Quadlet Extension

1. Update `internal/quadlet/quadlet.go` `ValidExtensions` slice
2. Verify `UnitNameFromQuadlet` logic still applies (all quadlets → `.service`)

### Fixing Linter Issues

```bash
make lint
# Review output and fix issues
# Re-run to verify
make lint
```

### Debugging Sync Issues

Run with debug logging:
```bash
quadsyncd sync --log-level debug --config config.yaml
```

Check state file:
```bash
cat ~/.local/state/quadsyncd/state.json
```

## Questions or Issues?

- Check docs/deployment-rootless-systemd.md for deployment help
- Review existing tests in `internal/*/`_test.go` for patterns
- Open GitHub issue if you find bugs or have feature requests

## Summary for Agents

When working on this codebase:
- Run `make fmt test lint` before committing
- Keep sync logic testable (use interfaces, avoid direct syscalls in core logic)
- Never commit secrets
- Follow systemd rootless conventions (user units, `~/.config/`, `~/.local/`)
- Webhook mode is future work; focus on oneshot sync stability first
