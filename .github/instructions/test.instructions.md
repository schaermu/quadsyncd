---
applyTo: "**/*_test.go"
---

# Test file instructions (quadsyncd)

Rules for all test files in this project:

- **No network calls:** All tests must be fully offline. Use temp directories and mock
  implementations instead of real git remotes, real systemd, or real Podman.
- **Mock via interfaces:** Side effects (git, systemd, filesystem) are behind interfaces.
  Implement fakes inline or in `internal/testutil/` to satisfy those interfaces.
- **Temp dirs:** Use `t.TempDir()` for any filesystem work; it is cleaned up automatically.
- **Race detector:** Tests run with `-race` in CI. Avoid shared mutable state across
  goroutines without synchronization.
- **Table-driven tests:** Prefer `for _, tc := range []struct{...}{...}` style for
  multiple related cases over separate `TestFoo_bar` functions.
- **Error paths:** Always test error conditions as well as the happy path.
- **No `t.Fatal` after goroutine spawns:** Use channels or `sync.WaitGroup` to
  collect results; only call `t.Fatal`/`t.Error` from the test goroutine.
- **Package naming:** Use `package foo_test` (external test package) for black-box
  tests; use `package foo` only when testing unexported helpers.
