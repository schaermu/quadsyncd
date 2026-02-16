---
applyTo: "**/*.go"
---

# Go-specific review instructions (quadsyncd)

Review Go changes with these project rules:

- Prefer small, testable functions. Keep core logic pure; isolate side effects behind
  interfaces (git/systemd/filesystem).
- Unit tests must not perform network operations. Use temp directories and mocks.
- Logging must use `log/slog` with structured fields. Avoid printf-style logging.
- Errors must carry context and wrap with `%w` where appropriate. Avoid panics in
  library/internal packages.
- Rootless invariants: systemd calls must be `systemctl --user` only; paths must be
  user-scoped (`~/.config`, `~/.local`) and absolute after expansion/validation.
- Do not introduce secret material in code, tests, or fixtures. Prefer `*_file`
  config fields for any sensitive values.
- Keep formatting gofmt-clean; avoid patterns that are likely to trip golangci-lint
  (unchecked errors, unused vars, etc.).
