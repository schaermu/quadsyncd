# Release Process

This guide documents the release process for quadsyncd maintainers.

## Prerequisites

Before creating a release, ensure:

1. All intended changes are merged to the `main` branch
2. CI is passing on `main` (see `.github/workflows/ci.yml`)
3. You have push access to the repository

## Pre-Release Validation

Run the full validation loop locally to catch any issues before tagging:

```bash
go mod tidy && git diff --exit-code && make lint && make test && make vuln
```

All checks must pass before proceeding.

## Tagging a Release

Quadsyncd uses semantic versioning with a `v` prefix. Tag format: `v<major>.<minor>.<patch>`

Examples:
- `v0.1.0` - Initial release
- `v0.2.0` - New features (minor bump)
- `v0.2.1` - Bug fixes (patch bump)
- `v1.0.0` - First stable release (major bump)

### Create and Push Tag

```bash
# Ensure you're on main and up-to-date
git checkout main
git pull origin main

# Create annotated tag
git tag -a v0.1.0 -m "Release v0.1.0"

# Push tag to trigger release workflow
git push origin v0.1.0
```

**Important:** Use annotated tags (`-a`) not lightweight tags. The release workflow is triggered by pushing tags matching `v*`.

## Automated Release Workflow

Once you push a tag, GitHub Actions automatically:

1. Runs the release workflow (see `.github/workflows/release.yml`)
2. Uses GoReleaser to build cross-platform binaries
3. Creates archives with the following contents (see `.goreleaser.yaml`):
   - Binary (`quadsyncd`)
   - `LICENSE`
   - `README.md`
   - `config.example.yaml`
   - `packaging/systemd/user/*` (systemd units)
4. Generates `checksums.txt` for all artifacts
5. Creates a GitHub Release with:
   - Auto-generated changelog (excludes commits with types: `docs`, `test`, `chore`, `ci`)
   - All build artifacts attached

### Supported Platforms

GoReleaser builds for the following platforms (see `.goreleaser.yaml`):

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`

All binaries are statically linked (`CGO_ENABLED=0`).

## Post-Release Verification

After the workflow completes:

1. Check the [Releases page](https://github.com/schaermu/quadsyncd/releases)
2. Verify all expected artifacts are present:
   - Archives for each platform (`.tar.gz`)
   - `checksums.txt`
3. Verify the changelog is reasonable
4. Download one archive and test installation:

```bash
# Example for linux/amd64
wget https://github.com/schaermu/quadsyncd/releases/download/v0.1.0/quadsyncd_0.1.0_Linux_x86_64.tar.gz
tar xzf quadsyncd_0.1.0_Linux_x86_64.tar.gz
./quadsyncd version
```

## Changelog Generation

The changelog is auto-generated from commit messages (see `.goreleaser.yaml`). Commits are included based on their conventional commit type:

**Included:**
- `feat`: New features
- `fix`: Bug fixes
- `refactor`: Refactorings
- `build`: Build changes

**Excluded:**
- `docs`: Documentation
- `test`: Test changes
- `chore`: Maintenance
- `ci`: CI changes

This is why following Conventional Commits is critical (see `CONTRIBUTING.md`).

## Troubleshooting

### Release Workflow Fails

1. Check the Actions tab for error logs
2. Common issues:
   - Build failures (run `make build` locally first)
   - GoReleaser config errors (validate `.goreleaser.yaml`)
   - Permission issues (ensure `GITHUB_TOKEN` has write access)

### Need to Re-Release

If you need to fix a release:

1. Delete the tag locally and remotely:
   ```bash
   git tag -d v0.1.0
   git push origin :refs/tags/v0.1.0
   ```
2. Delete the GitHub Release via the web UI
3. Fix the issue and create a new tag

**Note:** Avoid re-releasing with the same version tag. If the release has been published, increment to a patch version instead (e.g., `v0.1.0` → `v0.1.1`).

## Version Information in Binary

The build process injects version information via ldflags (see `.goreleaser.yaml`):

- `main.version`: Git tag (e.g., `v0.1.0`)
- `main.commit`: Git commit hash
- `main.date`: Build timestamp

This information is displayed via `quadsyncd version`.

## Release Checklist

- [ ] All changes merged to `main`
- [ ] CI passing on `main`
- [ ] Local validation loop passes (`go mod tidy && make lint && make test && make vuln`)
- [ ] Tag created with correct semantic version
- [ ] Tag pushed to GitHub
- [ ] Release workflow completed successfully
- [ ] GitHub Release created with all artifacts
- [ ] Changelog is reasonable
- [ ] One artifact manually tested
- [ ] Update documentation if needed (README, deployment guides)

## Source of Truth

This document provides narrative guidance. For authoritative configuration, always refer to:

- `.github/workflows/release.yml` - Release workflow definition
- `.goreleaser.yaml` - Build and archive configuration
- `Makefile` - Local build targets
