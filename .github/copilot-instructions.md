# Copilot repository instructions (quadsyncd)

You are reviewing changes in `quadsyncd`, a Go CLI that syncs Podman Quadlet files
from a Git repository to a rootless Podman + systemd user environment.

## What matters most in review

- Rootless only: systemd interactions must use `systemctl --user` and operate in
  user-owned paths under `~/.config` and `~/.local`. Never introduce root/system
  service assumptions.
- Never commit secrets: do not add tokens/keys/secrets to the repo. Config supports
  file-based secret references:
  - `auth.ssh_key_file`
  - `auth.https_token_file`
  - `serve.github_webhook_secret_file`
- Tests: unit tests must not make network calls. Prefer temp dirs and mock
  implementations for side effects (git/systemd/filesystem).
- Logging: use `log/slog` structured logging (`logger.Info(..., "key", val)`).
- Errors: return errors with context (`fmt.Errorf("...: %w", err)`); avoid panics in
  non-main code.

## Where to look

- CLI entrypoint/commands: `cmd/quadsyncd/main.go`
- Sync engine: `internal/sync/*`
- Config parsing/validation: `internal/config/*` (paths must be absolute after env expansion)
- Side effects behind interfaces:
  - git client: `internal/git/*`
  - systemd user ops: `internal/systemduser/*`
- Quadlet discovery/unit naming: `internal/quadlet/*`
- systemd user unit files: `packaging/systemd/user/*`

## Validation expectations (match CI)

CI definition is authoritative: `.github/workflows/ci.yml`.

Key constraints to enforce in review:
- `go mod tidy` must not produce a diff (CI fails otherwise).
- Tests run with race detector in CI.
- Lint runs via golangci-lint; avoid introducing new lints.

Local helpers exist in `Makefile` (`make fmt`, `make test`, `make lint`, `make vuln`, `make build`).

## Sync/domain invariants to sanity check

- Managed file tracking + pruning: only remove files quadsyncd manages; be careful
  with delete semantics when `sync.prune` is enabled.
- Atomic writes: writes to quadlet dir should remain atomic (temp + rename).
- Restart policy semantics must stay consistent:
  - `none`: reload only
  - `changed`: try-restart affected units
  - `all-managed`: try-restart all managed units
- Webhook mode (if touched): must bind localhost-only (`127.0.0.1`) and verify
  GitHub signatures before processing; reject invalid/unsigned requests early.
