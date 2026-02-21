# Contributing to quadsyncd

Thank you for your interest in contributing to quadsyncd! This guide will help you get started with development.

## Development Workflow

### Prerequisites

- Go 1.26 or later (check `go.mod` for the exact version)
- Git
- golangci-lint (installed via `go run` in Makefile)
- govulncheck (installed via `go run` in Makefile)

### Local Setup

Clone the repository and install dependencies:

```bash
git clone https://github.com/schaermu/quadsyncd.git
cd quadsyncd
go mod tidy
```

### Available Make Targets

See the `Makefile` for all available targets. Common commands:

```bash
make fmt      # Format code with gofmt
make test     # Run tests with race detector
make lint     # Run golangci-lint
make vuln     # Check for vulnerabilities with govulncheck
make build    # Build binary
make install  # Install to ~/.local/bin and copy systemd units
```

### Running Locally

Create a test configuration:

```bash
cp config.example.yaml config.yaml
# Edit config.yaml with test repo details
```

Test with dry-run mode:

```bash
go run ./cmd/quadsyncd sync --config config.yaml --dry-run
```

Run actual sync (requires git and systemctl --user):

```bash
go run ./cmd/quadsyncd sync --config config.yaml
```

### Testing Against Rootless Podman

The sync engine is designed for rootless Podman + systemd user units. For local testing:

1. Ensure systemd user session is active: `systemctl --user status`
2. Quadlet directory: `~/.config/containers/systemd/`
3. State directory: `~/.local/state/quadsyncd/`
4. After sync, verify Podman's generator created units: `systemctl --user list-units | grep container`

## Commit Guidelines

We follow strict commit discipline to maintain a clean, reviewable history.

### Atomic Commits

Each commit should represent **one discrete, logical change**:

- Do not mix unrelated changes (e.g., drive-by refactors, formatting-only edits, or dependency bumps) with feature/bugfix work
- Each commit should be independently reviewable
- If you're fixing a bug and refactoring code, make two separate commits

### Conventional Commits

All commit messages must follow the [Conventional Commits](https://www.conventionalcommits.org/) format:

**Format:** `type(scope?): subject`

**Allowed types:**
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `refactor`: Code refactoring (no functional changes)
- `test`: Adding or updating tests
- `chore`: Maintenance tasks
- `ci`: CI/CD changes
- `build`: Build system changes

**Subject rules:**
- Use imperative mood ("add feature" not "added feature")
- Keep it short and lowercase
- No trailing period
- Scope is optional but encouraged (e.g., `sync`, `config`, `git`)

**Body (optional):**
- Use the body to explain the "why" when it's not obvious from the subject
- Wrap at 72 characters

**Examples:**

```
fix(sync): handle missing quadlet dir

docs: clarify rootless systemd prerequisites

refactor(config): isolate validation helpers

feat(webhook): add github signature verification
```

### Pre-Commit Validation Loop

**Before each commit**, run the validation loop and fix all failures:

```bash
go mod tidy && git diff --exit-code && make lint && make test && make vuln
```

Run `make fmt` as needed (especially before lint) to keep diffs clean.

## Testing Expectations

### Unit Tests

- Keep logic testable by isolating side effects behind interfaces
- **No network calls in unit tests** - use temp directories and mock implementations
- Use the race detector: `go test -race ./...`
- See existing tests in `internal/*/` for patterns (especially `internal/sync/sync_test.go`)

### Key Interfaces for Testing

When adding functionality that interacts with external systems, define interfaces:

- `git.Client`: Clone/fetch/checkout operations
- `systemduser.Systemd`: daemon-reload, unit restarts
- Filesystem operations: Consider adding abstractions for plan/apply logic

### Test Coverage

While we don't enforce a specific coverage percentage, new features should include:

- Unit tests for core logic
- Edge case handling (empty dirs, missing files, permission errors)
- Error path testing

## Pull Request Process

1. Fork the repository and create a feature branch
2. Make your changes following the commit guidelines above
3. Ensure all validation checks pass (lint, test, vuln)
4. Push your branch and open a pull request
5. CI will automatically run tests and checks (see `.github/workflows/ci.yml`)
6. Address any review feedback with new commits (do not force-push during review)

## Continuous Integration

Our CI pipeline runs on every pull request and push to main. See `.github/workflows/ci.yml` for the authoritative definition. Key checks:

- `go mod tidy` verification (must not produce diff)
- `go test -race ./...` with coverage
- `govulncheck ./...`
- `golangci-lint run`
- Cross-platform builds (linux/amd64, linux/arm64, darwin/arm64)

Do not restate CI steps in documentation; always refer to the workflow file as the source of truth.

## Architecture Guidelines

### Testability First

Keep sync logic pure and testable:

- Isolate side effects (git, filesystem, systemctl) behind interfaces
- Avoid direct syscalls in core business logic
- Return errors with context; avoid panics

### Error Handling

- Always return errors with context (use `fmt.Errorf` with `%w` for wrapping)
- Never panic in library code (only in main/init if absolutely necessary)
- Log errors with structured fields before returning them

### Logging

Use `log/slog` (stdlib) with structured logging:

```go
logger.Info("syncing files", "source", src, "dest", dst)
logger.Error("sync failed", "error", err)
```

Log levels: debug, info, warn, error (configurable via `--log-level`).

## Security

- **Never commit secrets** (SSH keys, tokens, webhook secrets)
- Use `*_file` config fields for sensitive data
- Report security issues via the process documented in `SECURITY.md`

## Questions?

- Check the [wiki](https://github.com/schaermu/quadsyncd/wiki) for documentation
- Review existing code and tests for patterns
- Open a GitHub issue for bugs or feature requests
- See `AGENTS.md` if you're an AI coding agent (different audience)

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
