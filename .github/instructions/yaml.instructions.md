---
applyTo: "**/*.{yaml,yml}"
---

# YAML file instructions (quadsyncd)

Rules for all YAML files in this project:

## Configuration files (`config*.yaml`)

- **No secrets inline:** Never embed tokens, passwords, or SSH key material directly.
  Use the corresponding `*_file` field to point to a file outside the repository:
  - `auth.ssh_key_file`
  - `auth.https_token_file`
  - `serve.github_webhook_secret_file`
- **Environment variables:** Paths may use `${HOME}` / `${VAR}` expansion. All paths
  must resolve to absolute paths after expansion.
- **Mirror `config.example.yaml`:** When adding a new field to `config.go`, add a
  matching commented-out example entry to `config.example.yaml`.

## Systemd unit files (`packaging/systemd/user/*.{service,timer,socket}`)

- Units must run as the logged-in user (no `User=` override, no `sudo`).
- `ExecStart` must reference the binary by the path installed by `make install`
  (`~/.local/bin/quadsyncd`).
- Timer units must pair with a matching `.service` unit in the same directory.

## GitHub Actions workflows (`.github/workflows/*.yml`)

- Keep workflow files minimal; delegate logic to `Makefile` targets.
- Do not duplicate CI steps in documentation; the workflow file is the source of truth.
- Pin action versions with a full SHA or a pinned tag (e.g., `actions/checkout@v6`).
