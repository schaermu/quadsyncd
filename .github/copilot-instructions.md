# Copilot repository instructions (quadsyncd)

`quadsyncd` is a Go CLI that syncs Podman Quadlet files from a Git repository to a
rootless Podman + systemd user environment.

## Non-negotiable rules

- **Rootless only:** all systemd calls must use `systemctl --user`; paths must live
  under `~/.config` or `~/.local`. Never assume root or system-level services.
- **No secrets in code:** never embed tokens, keys, or passwords. Use `*_file` config
  fields (`auth.ssh_key_file`, `auth.https_token_file`,
  `serve.github_webhook_secret_file`).
- **No network in unit tests:** use `t.TempDir()` and mock interfaces instead.
- **Structured logging:** always use `log/slog` with key/value pairs.
- **Errors with context:** wrap with `fmt.Errorf("...: %w", err)`; no panics in
  `internal/` packages.
- **Conventional Commits:** commit messages must follow `type(scope): subject` format.

## Codebase map

| Area | Path |
|---|---|
| CLI entrypoint | `cmd/quadsyncd/main.go` |
| Sync engine | `internal/sync/` |
| Config parsing + validation | `internal/config/` |
| Git client interface | `internal/git/` |
| systemd user interface | `internal/systemduser/` |
| Quadlet discovery + unit naming | `internal/quadlet/` |
| Test helpers / mocks | `internal/testutil/` |
| systemd unit files | `packaging/systemd/user/` |

## Validation (match CI)

CI is authoritative: see `.github/workflows/ci.yml`. Local equivalents via `Makefile`:

```bash
go mod tidy && git diff --exit-code && make fmt && make lint && make test && make vuln
```

## Domain invariants

- **Pruning:** only remove files quadsyncd itself wrote (tracked in state). Be careful
  when `sync.prune` is enabled.
- **Atomic writes:** quadlet dir writes must use a temp file + rename.
- **Restart policy:**
  - `none` → reload only
  - `changed` → `try-restart` units whose quadlet changed
  - `all-managed` → `try-restart` every managed unit
- **Webhook (future):** bind `127.0.0.1` only; verify HMAC-SHA256 signature before
  any processing; reject unsigned/invalid requests with HTTP 403.

## Reusable prompts

Common tasks have step-by-step prompt files in `.github/prompts/`:

- **Add a config field** → `.github/prompts/add-config-field.prompt.md`
- **Add a quadlet extension** → `.github/prompts/add-quadlet-extension.prompt.md`

## Scoped instructions

File-type-specific rules live in `.github/instructions/`:

- `go.instructions.md` – applies to `**/*.go`
- `test.instructions.md` – applies to `**/*_test.go`
- `yaml.instructions.md` – applies to `**/*.{yaml,yml}`
