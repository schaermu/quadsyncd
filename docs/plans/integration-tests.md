# Integration Tests Implementation Plan

**Status:** Planning  
**Target:** Tier 1 (fast container-realistic integration) + Tier 2 Discovery E2E (Fedora systemd-in-container)  
**Future:** Tier 2 Runtime Workflow (manual initially)

---

## Overview

Integration testing for quadsyncd uses a two-tier approach:

**Tier 1 (PR CI - fast):** Container-realistic integration tests that validate quadsyncd's behavior (git → filesystem → state) and the exact systemd commands it would run, without requiring a full systemd user manager. Uses a `systemctl` shim to record invocations.

**Tier 2 (Nightly/Manual - comprehensive):** True E2E tests in a Fedora systemd-in-container environment with real `systemctl --user` + Podman Quadlet generator. Initially discovery-only (no unit starts); later add runtime tests (unit start + containers running) as a separate workflow.

---

## Tier 1: Container-Realistic Integration Tests

### Goal
Validate quadsyncd's end-to-end sync behavior (git checkout, filesystem operations, state tracking) and verify the exact `systemctl --user` commands it would invoke, without depending on a real systemd user manager.

### Strategy
- Run quadsyncd in a lightweight container with git + filesystem
- Provide a `systemctl` shim in PATH that records calls and returns configured exit codes
- Use a local git repo (no network)
- Assert on: quadlet files written/updated/deleted, state.json correctness, systemd command invocations recorded

### Container Design (Tier 1 SUT)
- Base: Alpine or Debian slim (no systemd required)
- Contains: `git`, `quadsyncd` binary, `systemctl` shim script
- Local git repo inside container (no network)

### systemctl Shim Requirements
Script installed at `/usr/local/bin/systemctl` (ahead of real systemctl in PATH):
- Records all invocations with arguments to a log file
- Returns success (exit 0) by default
- Supports environment variable overrides for exit codes (e.g., `SYSTEMCTL_DAEMON_RELOAD_EXIT=0`)
- Example implementation:
  ```bash
  #!/bin/sh
  echo "$(date -Iseconds) $@" >> /tmp/systemctl.log
  
  # Support exit code overrides
  case "$*" in
    *daemon-reload*)
      exit ${SYSTEMCTL_DAEMON_RELOAD_EXIT:-0}
      ;;
    *try-restart*)
      exit ${SYSTEMCTL_TRY_RESTART_EXIT:-0}
      ;;
    *)
      exit 0
      ;;
  esac
  ```

### Tier 1 Scenarios
Run as subtests within a single container lifecycle:

#### A) Initial sync with restart policy "none"
- **Config:** `sync.restart: none`
- **Run:** `quadsyncd sync`
- **Assert:**
  - quadlet file exists in `paths.quadlet_dir`
  - `state.json` exists with correct hash and commit
  - shim log contains `--user daemon-reload`
  - shim log does NOT contain `try-restart`

#### B) Initial sync with restart policy "changed"
- **Config:** `sync.restart: changed`
- **Run:** `quadsyncd sync`
- **Assert:**
  - quadlet file exists
  - state.json correct
  - shim log contains `--user daemon-reload`
  - shim log contains `--user try-restart hello.service` (derived from `hello.container`)

#### C) Update sync with restart policy "changed"
- **Arrange:** Modify quadlet content, commit
- **Run:** `quadsyncd sync`
- **Assert:**
  - destination file content changed
  - state hash updated
  - shim log contains `--user daemon-reload`
  - shim log contains `--user try-restart hello.service`

#### D) No-op sync (no changes)
- **Arrange:** No repo changes since last sync
- **Run:** `quadsyncd sync`
- **Assert:**
  - no file operations
  - state unchanged
  - shim log contains `--user daemon-reload`
  - no try-restart calls

#### E) Prune removes file
- **Arrange:** Delete quadlet from repo, commit; `sync.prune: true`
- **Run:** `quadsyncd sync`
- **Assert:**
  - destination file removed
  - state no longer references the file
  - shim log contains `--user daemon-reload`
  - shim log contains `--user try-restart hello.service`

#### F) Dry-run mode
- **Run:** `quadsyncd sync --dry-run`
- **Assert:**
  - no files written
  - state unchanged
  - shim log empty (no systemctl calls)
  - stdout contains plan details

### Tier 1 Test Structure
- Location: `integration/tier1/`
- Build tag: `//go:build integration`
- Test files:
  - `integration/tier1/sync_test.go`
  - `integration/tier1/harness.go` (container orchestration helpers)
  - `integration/tier1/shim.go` (systemctl shim generation)
- Docker image:
  - `integration/tier1/docker/Dockerfile`

### Tier 1 Test Harness
Simpler than Tier 2 (no systemd boot complexity):
- `BuildImage()`: build alpine + git + quadsyncd + shim
- `StartContainer()`: simple `docker run -d`
- `Exec()`: run commands inside container
- `ReadShimLog()`: parse `/tmp/systemctl.log`
- `WriteFile()`: inject files into container
- `Cleanup()`: remove container unless `INTEGRATION_KEEP_CONTAINER=1`

### Tier 1 CI Integration
Add to existing `.github/workflows/ci.yml`:
```yaml
- name: Integration Tests (Tier 1)
  run: go test -tags=integration ./integration/tier1 -v
```

Runs on every PR (fast, no systemd complexity).

### Tier 1 vs Unit Tests
- **Unit tests** (`internal/*/`): mock interfaces, temp dirs, no git/systemctl at all
- **Tier 1**: real git, real filesystem, real quadsyncd binary, shimmed systemctl
- **Tier 2**: real systemctl --user, real systemd, real Quadlet generator

---

## Tier 2: Discovery E2E (Fedora systemd-in-container)

### Goal
Nightly/manual integration tests that run quadsyncd in a close-to-real environment (real `systemctl --user` + Podman Quadlet generator) without starting units/containers. Docker is the outer orchestrator. One container per suite.

---

## Tier 2 Scope and Success Criteria

### In-scope (Tier 2 discovery)
- Boot Fedora container with systemd as PID1.
- Create and use non-root user `quadsync` (UID 1000).
- Make `systemctl --user` usable for that user (readiness probe).
- Create a local git repo inside the container (no network Git).
- Run `quadsyncd sync` against that repo (`repo.ref: main`).
- Assert:
  - quadlet file present/updated/removed in `~/.config/containers/systemd`
  - state file present in `~/.local/state/quadsyncd/state.json`
  - generated unit exists: `systemctl --user cat hello.service` succeeds (or fails after prune)

### Out-of-scope for discovery
- `systemctl --user start ...`, running containers, image pulls.
- SSH/HTTPS auth against remote Git servers.
- `repo.subdir` coverage (defer until discovery suite is stable).

### Acceptance criteria
- `go test -tags=e2e_discovery ./e2e -v` passes locally and in a nightly workflow.
- On failure, CI uploads a diagnostics bundle that makes the cause obvious (systemd/user manager/dbus vs quadsyncd vs git).

---

## Repository Additions

### E2E code
- `e2e/`
- `e2e/harness/`
  - `e2e/harness/suite.go`
  - `e2e/harness/docker.go`
  - `e2e/harness/diag.go`
- `e2e/discovery_test.go` (build tag `//go:build e2e_discovery`)

### Container image
- `e2e/docker/sut-systemd/`
  - `e2e/docker/sut-systemd/Dockerfile`

### CI
- `.github/workflows/e2e-discovery.yml` (nightly + workflow_dispatch)
- `.github/workflows/e2e-runtime.yml` (separate; workflow_dispatch only initially; can be stubbed or added when runtime tests exist)

### Docs
- [wiki: Integration Tests](https://github.com/schaermu/quadsyncd/wiki/Integration-Tests)
- Optional: small pointer added to `CONTRIBUTING.md` that links to the wiki (no CI step restatement; link to workflows)

---

## Container Design (Fedora systemd SUT)

### Dockerfile requirements
Multi-stage build:
1) **Builder stage:** compile `quadsyncd` for Linux.
2) **Runtime stage:** Fedora + systemd + dbus + git + podman (+ future-proof rootless deps).

Install packages (Fedora equivalents):
- `systemd`, `dbus`, `git`, `podman`
- future runtime: `uidmap`, `slirp4netns`, `fuse-overlayfs`

Container setup:
- Create user `quadsync` with UID/GID 1000.
- Configure subuid/subgid ranges for `quadsync` (future runtime).
- Copy `quadsyncd` to `/usr/local/bin/quadsyncd`.
- `CMD ["/sbin/init"]`
- `STOPSIGNAL SIGRTMIN+3`

### Docker run flags (suite harness must use)
```bash
--privileged
--cgroupns=host
-v /sys/fs/cgroup:/sys/fs/cgroup:rw
--tmpfs /run
--tmpfs /tmp
-e container=docker
```

---

## Suite Harness API

### Suite object structure
```go
type Suite struct {
  // immutable config
  Name          string
  ImageTag      string
  DockerfileDir string
  Timeout       time.Duration
  KeepContainer bool

  // runtime state
  ContainerID string
  UID         int    // 1000
  User        string // "quadsync"
  Home        string // "/home/quadsync"

  // computed env for user exec
  UserEnv map[string]string // HOME, XDG_RUNTIME_DIR, (optional) DBUS_SESSION_BUS_ADDRESS

  // optional logger hook
  Logf func(format string, args ...any)
}
```

### Lifecycle methods
- `NewSuite(name string, opts ...SuiteOption) *Suite`
- `BuildImage(ctx context.Context) error`
- `StartContainer(ctx context.Context) error`
- `StopAndRemove(ctx context.Context) error` (respects KeepContainer)
- `Ready(ctx context.Context) error` (most important stability logic)

### Execution helpers
```go
type ExecResult struct {
  Cmd      []string
  ExitCode int
  Stdout   string
  Stderr   string
}

func (s *Suite) ExecRoot(ctx context.Context, cmd ...string) (ExecResult, error)
func (s *Suite) ExecUser(ctx context.Context, cmd ...string) (ExecResult, error)
func (s *Suite) ExecUserEnv(ctx context.Context, env map[string]string, cmd ...string) (ExecResult, error)

func (s *Suite) MustExecRoot(ctx context.Context, cmd ...string) ExecResult
func (s *Suite) MustExecUser(ctx context.Context, cmd ...string) ExecResult
```

### Provisioning primitives
- `WriteFileRoot(ctx context.Context, path string, content []byte, mode os.FileMode) error`
- `WriteFileUser(ctx context.Context, path string, content []byte, mode os.FileMode) error`
- `MkdirUser(ctx context.Context, path string, mode os.FileMode) error`

### Diagnostics
```go
type Diagnostics struct {
  CollectedAt time.Time
  Items       []DiagItem
}

type DiagItem struct {
  Name     string
  Cmd      []string
  ExitCode int
  Output   string
}

func (s *Suite) CollectDiagnostics(ctx context.Context) (*Diagnostics, error)
func (s *Suite) DumpDiagnostics(ctx context.Context)
func (s *Suite) RunScenario(ctx context.Context, name string, fn func(context.Context) error) error
```

### Suite options
- `WithImageTag(tag string) SuiteOption`
- `WithDockerfileDir(dir string) SuiteOption`
- `WithTimeout(d time.Duration) SuiteOption`
- `WithKeepContainer(v bool) SuiteOption`
- `WithUser(user string, uid int, home string) SuiteOption`
- `WithLogf(logf func(string, ...any)) SuiteOption`

---

## Readiness Probe (Critical Stability Logic)

`Suite.Ready(ctx)` must run in order:

### 1) Wait for PID1 systemd
- Command: `systemctl is-system-running --wait`
- Timeout: bounded by suite timeout
- Accept `degraded` status

### 2) Ensure user manager exists
```bash
loginctl enable-linger quadsync
systemctl start user@1000.service
```

### 3) Ensure runtime dir exists
- Create `/run/user/1000` if needed
- `chown 1000:1000`, `chmod 0700`

### 4) Establish user exec env
```bash
HOME=/home/quadsync
XDG_RUNTIME_DIR=/run/user/1000
```

If `/run/user/1000/bus` exists:
```bash
DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus
```

### 5) Definitive probe (must succeed)
As `quadsync` with env above:
```bash
systemctl --user status
```

If probe fails: collect diagnostics immediately and abort the suite.

---

## Discovery Suite Provisioning

Provision once per suite as `quadsync`:

### Local git repo
- Path: `/home/quadsync/repo`
- Initialize repo, create branch `main`, commit

### Quadlet (repo root)
File: `hello.container`
```ini
[Unit]
Description=quadsyncd e2e hello

[Container]
ContainerName=quadsyncd-e2e-hello
Image=alpine:3.20
Exec=/bin/sleep 3600
```

### Quadsyncd config
Path: `/home/quadsync/.config/quadsyncd/config.yaml`
```yaml
repo:
  url: /home/quadsync/repo
  ref: main
  subdir: ""

paths:
  quadlet_dir: /home/quadsync/.config/containers/systemd
  state_dir: /home/quadsync/.local/state/quadsyncd

sync:
  prune: true
  restart: none

auth:
  ssh_key_file: /dev/null
```

**Note:** `auth.ssh_key_file` required by current validation; unused for local repo paths.

---

## Discovery Scenarios

Run as subtests within a single suite test. Do not start/stop the container per subtest.

### A) Initial sync generates unit
- **Run:** `quadsyncd sync --config ~/.config/quadsyncd/config.yaml`
- **Assert:**
  - `~/.config/containers/systemd/hello.container` exists
  - `~/.local/state/quadsyncd/state.json` exists
  - `systemctl --user cat hello.service` succeeds

### B) Update keeps unit
- **Arrange:** Modify `hello.container` in repo (e.g., change Description), commit to `main`
- **Run:** `quadsyncd sync`
- **Assert:**
  - destination file content changed
  - `systemctl --user cat hello.service` still succeeds

### C) Prune removes unit
- **Arrange:** Delete `hello.container` from repo, commit
- **Run:** `quadsyncd sync` (with prune true)
- **Assert:**
  - destination quadlet file removed
  - `systemctl --user cat hello.service` fails

---

## Diagnostics Bundle

Collect best-effort outputs into files (host-side temp dir) and upload on failure:

### Root (inside container)
- `systemctl status --no-pager`
- `systemctl status user@1000.service --no-pager`
- `loginctl user-status quadsync --no-pager`
- `journalctl -b --no-pager -n 300`

### User (as `quadsync`, with XDG env)
- `systemctl --user status --no-pager`
- `journalctl --user --no-pager -n 300`
- `systemctl --user cat hello.service --no-pager`
- `ls -la ~/.config/containers/systemd`
- `ls -la ~/.local/state/quadsyncd`
- `cat ~/.local/state/quadsyncd/state.json` (if present)

### Outer
- `docker logs <container>`

---

## CI Workflows

### `.github/workflows/e2e-discovery.yml`
- **Triggers:** `schedule` + `workflow_dispatch`
- **Runs:** `go test -tags=e2e_discovery ./e2e -v`
- **On failure:** upload diagnostics artifact directory

### `.github/workflows/e2e-runtime.yml` (separate, later)
- **Trigger:** `workflow_dispatch` only initially
- **Runs:** `go test -tags=e2e_runtime ./e2e -v`
- **Purpose:** "start unit / podman running" assertions and image strategy; intentionally isolated from discovery workflow

**Important:** Docs must link to these workflow files as the source of truth (avoid duplicating CI steps).

---

## Documentation Plan

### Create wiki page: Integration Tests
- Explain Tier 1 vs Tier 2
- Explain Tier 2 split: discovery vs runtime workflows (and why)
- How to run locally:
  - `go test -tags=e2e_discovery ./e2e -v`
  - `E2E_KEEP_CONTAINER=1` for debugging
- Troubleshooting focused on:
  - `user@1000.service` status
  - `/run/user/1000/bus` existence
  - user journal outputs
- Link to `.github/workflows/e2e-discovery.yml` and `.github/workflows/e2e-runtime.yml`

### Optional: Update `CONTRIBUTING.md`
Add a short pointer to the [Integration Tests wiki page](https://github.com/schaermu/quadsyncd/wiki/Integration-Tests) (no CI step restatement; link to workflows).

---

## Execution Order (Recommended Milestones)

1. **Add SUT Dockerfile** and verify container boots systemd with the required run flags
2. **Implement harness** + `Ready()` probe + diagnostics collector
3. **Implement Scenario A only**; stabilize locally
4. **Add update and prune scenarios**
5. **Add `e2e-discovery.yml` workflow** and validate artifact upload on a forced failure
6. **Add wiki page: Integration Tests** (+ optional `CONTRIBUTING.md` pointer)
7. **Start runtime work** in parallel later under separate workflow/tag

---

## Environment Variables (for local debugging)

- `E2E_KEEP_CONTAINER=1`: Keep container around after failure/success
- `E2E_SUT_TAG=...`: Optional override for image tag
- `E2E_TIMEOUT=...`: Optional override for suite timeout

---

## Key Design Principles

1. **One container per suite** (not per test): fewer systemd boots, more stable, faster
2. **Fail fast on readiness probe**: don't attempt scenarios if `systemctl --user` doesn't work
3. **Always collect diagnostics**: on any failure, capture full systemd/user manager state
4. **Centralize docker operations**: all exec/file-writing in harness; tests stay clean
5. **Separate discovery from runtime**: keep nightly signal stable while runtime tests evolve
6. **No external dependencies**: local git repo, no image pulls in discovery tier

---

## Future: Tier 2 Runtime Tests

**When ready (separate workflow `e2e-runtime.yml`):**
- Add tests that:
  - run `systemctl --user start hello.service`
  - assert `is-active` becomes `active`
  - assert `podman ps` shows the expected container
- Decide image strategy:
  - start with `podman pull` (fast iteration)
  - later add offline mode (`podman load` from OCI archive)
- Keep separate from discovery workflow to avoid polluting nightly signal

---

## Notes

- **Why `repo.ref: main` in suite?** Most reliable input to current `git checkout -f <ref>` behavior in `internal/git/git.go`
- **Why Fedora?** Podman + quadlet generator + systemd tend to be least painful on Fedora
- **Why discovery-only initially?** Avoids biggest risk (image pulls/network/podman runtime) while still proving systemd user integration + quadlet generator works
- **Why separate workflows?** Keeps discovery stable and fast; runtime experiments don't block nightly
