# Integration Tests

This document describes quadsyncd's integration test strategy and how to run tests locally.

## Overview

quadsyncd uses a two-tier integration testing approach:

- **Tier 1 (PR CI):** Fast container-realistic tests that validate end-to-end sync behavior with a shimmed `systemctl`
- **Tier 2 (Nightly/Manual):** Comprehensive E2E tests in a Fedora systemd container with real `systemctl --user` and Podman Quadlet generator

This separation keeps PR CI fast while ensuring thorough validation in a close-to-production environment.

## Tier 1: Container-Realistic Integration Tests

### Purpose

Tier 1 tests validate quadsyncd's complete sync workflow (git checkout → filesystem operations → state tracking) and verify the exact `systemctl --user` commands it invokes, without requiring a full systemd environment.

### How It Works

- Runs quadsyncd in a lightweight Alpine container with git
- Provides a `systemctl` shim that records all invocations
- Uses a local git repository (no network dependencies)
- Asserts on: quadlet files written/updated/deleted, state.json correctness, systemd commands invoked

### Running Locally

```bash
# Run all Tier 1 tests
go test -tags=integration ./integration/tier1 -v

# Keep container on failure for debugging
INTEGRATION_KEEP_CONTAINER=1 go test -tags=integration ./integration/tier1 -v
```

### Test Scenarios

Tier 1 includes these scenarios:

- **A) Initial sync with restart:none** - Validates daemon-reload only, no unit restarts
- **B) Initial sync with restart:changed** - Validates daemon-reload + try-restart of new units
- **C) Update sync with restart:changed** - Validates detection of changes and restart
- **D) No-op sync** - Validates no operations when nothing changed
- **E) Prune removes file** - Validates deletion of removed quadlets
- **F) Dry-run mode** - Validates no side effects in dry-run

### CI Integration

Tier 1 tests run on every PR via `.github/workflows/ci.yml`:

```yaml
- name: Integration Tests (Tier 1)
  run: go test -tags=integration ./integration/tier1 -v
```

## Tier 2: Discovery E2E Tests

### Purpose

Tier 2 discovery tests validate quadsyncd in a close-to-real environment with:

- Real systemd as PID 1
- Real user systemd manager (`systemctl --user`)
- Real Podman Quadlet generator
- No unit starts or container execution (discovery phase only)

### How It Works

- Boots Fedora container with systemd
- Creates non-root user `quadsync` (UID 1000)
- Performs comprehensive readiness probe to ensure `systemctl --user` works
- Runs quadsyncd against a local git repo
- Validates quadlet files and generated systemd units

### Running Locally

```bash
# Run discovery tests
go test -tags=e2e_discovery ./e2e -v

# Keep container on failure
E2E_KEEP_CONTAINER=1 go test -tags=e2e_discovery ./e2e -v

# Custom timeout
E2E_TIMEOUT=15m go test -tags=e2e_discovery ./e2e -v
```

### Test Scenarios

Tier 2 discovery includes:

- **A) Initial sync generates unit** - Validates quadlet files and systemd unit generation
- **B) Update keeps unit** - Validates updates preserve unit functionality
- **C) Prune removes unit** - Validates deletion removes generated units

### Readiness Probe

The Tier 2 suite performs a critical readiness probe before running tests:

1. Wait for PID 1 systemd to be ready
2. Enable lingering for test user (`loginctl enable-linger`)
3. Start user systemd manager (`user@1000.service`)
4. Ensure XDG_RUNTIME_DIR exists and is correctly owned
5. Run definitive probe: `systemctl --user status`

If the probe fails, diagnostics are collected immediately and the suite aborts.

### CI Integration

Tier 2 discovery tests run nightly and can be manually triggered via `.github/workflows/e2e-discovery.yml`.

## Diagnostics

### Tier 1 Diagnostics

On failure with `INTEGRATION_KEEP_CONTAINER=1`:

```bash
# Inspect container
docker exec -it <container-id> /bin/sh

# View shim log
docker exec <container-id> cat /tmp/systemctl.log

# Cleanup when done
docker stop <container-id>
```

### Tier 2 Diagnostics

On failure, the suite automatically collects:

**Root diagnostics:**
- `systemctl status`
- `systemctl status user@1000.service`
- `loginctl user-status quadsync`
- `journalctl -b -n 300`
- Docker container logs

**User diagnostics:**
- `systemctl --user status`
- `journalctl --user -n 300`
- `systemctl --user cat hello.service`
- Directory listings of quadlet and state directories
- State file contents

With `E2E_KEEP_CONTAINER=1`, inspect manually:

```bash
# Get a shell as quadsync user
docker exec -it -u 1000:1000 \
  -e HOME=/home/quadsync \
  -e XDG_RUNTIME_DIR=/run/user/1000 \
  <container-id> /bin/bash

# Check user systemd status
systemctl --user status

# View user journal
journalctl --user -n 100

# Cleanup when done
docker stop <container-id>
```

## Troubleshooting

### Tier 1: Common Issues

**Problem:** Container fails to start
- Check Docker is running
- Ensure image built successfully: `docker images | grep quadsyncd-tier1-sut`

**Problem:** Git operations fail
- Check repo initialization in test output
- Verify git is installed in container

**Problem:** Systemctl shim not recording
- Check `/tmp/systemctl.log` in container
- Verify shim is in PATH before real systemctl

### Tier 2: Common Issues

**Problem:** `systemctl --user status` fails in readiness probe
- Check `user@1000.service` is running: `systemctl status user@1000.service`
- Verify `/run/user/1000` exists and has correct ownership (1000:1000)
- Check for dbus issues: `ls -la /run/user/1000/bus`
- Review user journal: `journalctl --user -n 100`

**Problem:** Quadlet generator doesn't run
- Verify podman is installed: `podman --version`
- Check quadlet file location: `ls -la ~/.config/containers/systemd`
- Run `systemctl --user daemon-reload` manually
- Check generator logs in user journal

**Problem:** Container boots slowly or times out
- Increase timeout: `E2E_TIMEOUT=20m go test ...`
- Check systemd is reaching "running" state
- Review systemd journal for errors

**Problem:** Test fails but container removed
- Use `E2E_KEEP_CONTAINER=1` to preserve container
- Check workflow artifact uploads for diagnostics

### Getting Help

For persistent issues:

1. Run with `E2E_KEEP_CONTAINER=1` or `INTEGRATION_KEEP_CONTAINER=1`
2. Collect diagnostics manually
3. Check the GitHub workflow runs for artifact uploads
4. Review systemd and journal logs for root cause

## Future: Runtime Tests

Tier 2 will eventually include runtime tests (separate workflow) that:

- Start units with `systemctl --user start`
- Validate containers are running with `podman ps`
- Test image pull strategies

These will run in a separate workflow (`.github/workflows/e2e-runtime.yml`) to keep discovery signal clean.

## Design Principles

1. **One container per suite** - Fewer systemd boots, more stable, faster
2. **Fail fast on readiness** - Don't run scenarios if systemctl doesn't work
3. **Always collect diagnostics** - Capture full state on any failure
4. **Separate discovery from runtime** - Keep nightly signal stable
5. **No external dependencies** - Use local git repos in tests
6. **Centralize docker operations** - Keep test code clean and focused

## References

- CI workflow: `.github/workflows/ci.yml`
- E2E discovery workflow: `.github/workflows/e2e-discovery.yml`
- Integration test plan: `docs/plans/integration-tests.md`
