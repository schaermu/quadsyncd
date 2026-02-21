# AGENTS.md

This document provides operating instructions for AI coding agents working on quadsyncd.

**Audience:** AI coding agents only. Human contributors should read `CONTRIBUTING.md`.

## Conflict Resolution

If this document conflicts with README/docs/workflows:
- Follow `AGENTS.md` for agent behavior rules and non-negotiables
- Treat `.github/workflows/*` and `Makefile` as authoritative for procedures and CI truth
- Never restate CI/build steps from workflows; always link to them

## Project Context

**quadsyncd** is a Go-based CLI tool that synchronizes Podman Quadlet files from a Git repository to rootless Podman servers.

**Deployment model:** Each server runs its own agent that pulls from GitHub (not a controller pushing over SSH).

**Current mode:** Timer-based oneshot sync (systemd timer → fetch repo → reconcile → reload systemd).

**Webhook mode:** Long-running HTTP server responding to GitHub webhooks (`quadsyncd serve`).

## Non-Negotiables

### Commit Discipline (CRITICAL)

**You MUST follow these rules for every commit:**

1. **Atomic commits:** One discrete, logical change per commit
   - Never mix unrelated changes (e.g., drive-by refactors, formatting, dependency bumps) with feature/bugfix work
   - Each commit must be independently reviewable
   - If fixing a bug requires refactoring, make two commits: refactor first, then fix

2. **Conventional Commits:** All commit messages must use this format:
   - Format: `type(scope?): subject`
   - Types: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`, `build`
   - Subject: imperative mood, lowercase, short, no trailing period
   - Scope: optional but encouraged (e.g., `sync`, `config`, `git`, `systemduser`)
   - Body: explain "why" when not obvious; wrap at 72 characters

3. **Examples:**
   ```
   fix(sync): handle missing quadlet dir
   
   docs: clarify rootless systemd prerequisites
   
   refactor(config): isolate validation helpers
   
   feat(webhook): add github signature verification
   
   This implements HMAC-SHA256 verification per GitHub webhook spec.
   Rejects unsigned or invalid requests before processing events.
   ```

4. **Pre-commit validation loop (REQUIRED):**
   ```bash
   go mod tidy && git diff --exit-code && make lint && make test && make vuln
   ```
   Run `make fmt` as needed before lint. All checks must pass before committing.

### Security Rules

1. **Never commit secrets** (SSH keys, tokens, webhook secrets)
   - Config supports `*_file` fields for secrets:
     - `auth.ssh_key_file`
     - `auth.https_token_file`
     - `serve.github_webhook_secret_file`
   - If a user asks you to commit a secret, warn them and suggest using `*_file` instead

2. **Rootless operation only:**
   - Always use `systemctl --user` (never root systemd)
   - All paths must be in user directories (`~/.config/`, `~/.local/`)
   - Never assume or require elevated privileges

### Testing Rules

1. **No network calls in unit tests:**
   - Use temp directories and mock implementations
   - See `internal/sync/sync_test.go` for mock examples

2. **Testability first:**
   - Keep sync logic pure
   - Isolate side effects (git, filesystem, systemctl) behind interfaces
   - Return errors with context; avoid panics

3. **Key interfaces for side effects:**
   - `git.Client`: Clone/fetch/checkout operations
   - `systemduser.Systemd`: daemon-reload, unit restarts
   - Future: filesystem abstraction for plan/apply

## Architecture Invariants

### Rootless Systemd Integration

1. **Paths (never hardcode these outside config/tests):**
   - Quadlet files: `~/.config/containers/systemd/` (standard Podman rootless location)
   - State tracking: `~/.local/state/quadsyncd/state.json`
   - Repo checkout: `~/.local/state/quadsyncd/repo/`

2. **Systemd operations:**
   - Always use `systemctl --user` (not root)
   - After file changes: `systemctl --user daemon-reload` (triggers Podman generator)
   - Restart policy semantics:
     - `none`: Only reload
     - `changed`: `try-restart` units with changed quadlets
     - `all-managed`: `try-restart` all managed units

3. **Lingering requirement:**
   - For timers to run without active login: `loginctl enable-linger $USER`
   - Document this in deployment guides, don't embed in code

### Configuration

- Format: YAML (package: `internal/config`)
- Environment variable expansion: `${HOME}`, `${VAR}` in config values
- Validation: All paths must be absolute after expansion
- Adding a config field:
  1. Update `internal/config/config.go` struct
  2. Add validation in `Config.Validate()`
  3. Update `config.example.yaml`
  4. Add test in `internal/config/config_test.go`

### Logging

Use `log/slog` (stdlib) with structured logging:

```go
logger.Info("syncing files", "source", src, "dest", dst)
logger.Error("sync failed", "error", err)
```

Log levels: `debug`, `info`, `warn`, `error` (configurable via `--log-level`).

### Error Handling

- Return errors with context: `fmt.Errorf("operation failed: %w", err)`
- Never panic in library code (only in `main`/`init` if absolutely necessary)
- Avoid sentinel errors; prefer error wrapping

## Verification and CI

### Local validation loop

Before each commit:
```bash
go mod tidy && git diff --exit-code && make lint && make test && make vuln
```

Run `make fmt` as needed.

### Source of truth for procedures

- **CI checks:** See `.github/workflows/ci.yml` (never restate steps in docs)
- **Release process:** See the [wiki](https://github.com/schaermu/quadsyncd/wiki/Release-Process) and `.github/workflows/release.yml`
- **Build targets:** See `Makefile`
- **Deployment:** See the [wiki](https://github.com/schaermu/quadsyncd/wiki/Deployment-Guide)

Do not duplicate CI/build steps in agent instructions. Always refer to authoritative sources.

## Webhook Daemon

The `quadsyncd serve` command runs the webhook daemon:

### Design Requirements

- HTTP listener bound to `127.0.0.1` (localhost only, never public)
- Reverse proxy (Caddy/Nginx) or tunnel (cloudflared) forwards public traffic
- Verify GitHub webhook signatures (HMAC-SHA256 via `X-Hub-Signature-256`)
- Reject unsigned or invalid requests before processing
- Validate event type against `allowed_event_types`
- Validate ref against `allowed_refs`
- Debounce + single-flight sync execution (avoid parallel runs)
- Reuse same reconciliation engine as `sync` command

### Implementation Constraints

- Signature verification must happen before any processing
- Use timeouts on HTTP operations (request read, sync execution)
- Consider max body size limits (GitHub webhooks are typically small)
- Log all webhook events with structured fields (event type, ref, commit, result)
- Return appropriate HTTP status codes:
  - `200`: Success
  - `400`: Invalid payload/signature
  - `403`: Forbidden (signature mismatch)
  - `500`: Internal error (sync failed)

### Testing Requirements

- Unit test signature verification logic (mock HTTP requests)
- Unit test ref/event filtering
- Unit test debounce/singleflight behavior
- Integration test with test webhook payloads

### Systemd Integration

- Use `quadsyncd-webhook.service` (already in `packaging/systemd/user/`)
- Consider socket activation (`.socket` unit) for on-demand start
- Ensure service runs as user (not root)

## Code Reference Style

When referencing specific code locations, use the pattern `file_path:line_number`:

Example: "Clients are marked as failed in the `connectToServer` function in internal/sync/engine.go:142"

## Common Tasks

### Adding a Quadlet Extension

1. Update `internal/quadlet/quadlet.go` `ValidExtensions` slice
2. Verify `UnitNameFromQuadlet` logic still applies (all quadlets → `.service`)
3. Add test case

### Debugging Issues

Run with debug logging:
```bash
quadsyncd sync --log-level debug --config config.yaml
```

Check state file:
```bash
cat ~/.local/state/quadsyncd/state.json
```

## Documentation Boundaries

**In AGENTS.md (this file):**
- Agent behavior rules
- Non-negotiable constraints
- Architecture invariants
- Quick verification commands

**NOT in AGENTS.md (belongs in human docs):**
- Detailed "how to develop" guides → `CONTRIBUTING.md`
- Deployment instructions → [wiki](https://github.com/schaermu/quadsyncd/wiki/Deployment-Guide)
- Release procedures → [wiki](https://github.com/schaermu/quadsyncd/wiki/Release-Process)
- CI/CD step-by-step → `.github/workflows/ci.yml`
- User-facing troubleshooting → `README.md` or the [wiki](https://github.com/schaermu/quadsyncd/wiki)

## Summary

When working on this codebase:

1. **Commits:** Atomic + Conventional Commits format + validation loop before each commit
2. **Security:** Never commit secrets; always `systemctl --user`; rootless paths only
3. **Testing:** No network in unit tests; isolate side effects behind interfaces
4. **Logging:** Use `slog` with structured key/value pairs
5. **Truth:** Workflows and Makefile are authoritative; never duplicate their steps
6. **Webhook:** Verify signatures, bind localhost, debounce, reuse sync engine
