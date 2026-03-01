package systemduser

// Tests in this file verify that Client methods construct the correct
// command-line invocations.  The technique used is the "fake binary in PATH"
// pattern: a tiny shell script is written to a temp directory which is
// prepended to PATH.  When the real Client calls exec.CommandContext the OS
// resolves the binary name to our script, which captures os.Args into a file.
// The test then reads that file and checks the recorded arguments.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeBinary writes a shell script to dir/<name> that saves its arguments
// (one per line) to dir/args.txt and exits 0.
func writeFakeBinary(t *testing.T, dir, name string) {
	t.Helper()
	argsFile := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$0\" \"$@\" > " + argsFile + "\n" +
		"exit 0\n"
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(script), 0755); err != nil {
		t.Fatalf("writeFakeBinary: %v", err)
	}
}

// readCapturedArgs reads the args captured by writeFakeBinary.
// Returns nil if the file does not exist (binary was never called).
func readCapturedArgs(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, "args.txt"))
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	// lines[0] is $0 (the binary path itself), lines[1:] are the real args.
	if len(lines) < 2 {
		return []string{}
	}
	return lines[1:]
}

// prependToPATH returns a cleanup func that restores the original PATH.
func prependToPATH(t *testing.T, dir string) {
	t.Helper()
	original := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+original)
}

// TestSystemd_DaemonReload_UsesUserScope verifies that DaemonReload invokes
// "systemctl --user daemon-reload".
func TestSystemd_DaemonReload_UsesUserScope(t *testing.T) {
	binDir := t.TempDir()
	writeFakeBinary(t, binDir, "systemctl")
	prependToPATH(t, binDir)

	c := NewClient(testLogger())
	if err := c.DaemonReload(context.Background()); err != nil {
		t.Fatalf("DaemonReload: %v", err)
	}

	args := readCapturedArgs(binDir)
	if args == nil {
		t.Fatal("systemctl was never called")
	}

	want := []string{"--user", "daemon-reload"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i, a := range want {
		if args[i] != a {
			t.Errorf("arg[%d] = %q, want %q", i, args[i], a)
		}
	}
}

// TestSystemd_TryRestartUnits_BuildsArgs verifies that TryRestartUnits passes
// --user try-restart followed by each unit name.
func TestSystemd_TryRestartUnits_BuildsArgs(t *testing.T) {
	binDir := t.TempDir()
	writeFakeBinary(t, binDir, "systemctl")
	prependToPATH(t, binDir)

	c := NewClient(testLogger())
	units := []string{"app.service", "db.service"}
	if err := c.TryRestartUnits(context.Background(), units); err != nil {
		t.Fatalf("TryRestartUnits: %v", err)
	}

	args := readCapturedArgs(binDir)
	if args == nil {
		t.Fatal("systemctl was never called")
	}

	want := []string{"--user", "try-restart", "app.service", "db.service"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i, a := range want {
		if args[i] != a {
			t.Errorf("arg[%d] = %q, want %q", i, args[i], a)
		}
	}
}

// TestSystemd_ValidateQuadlets_UsesQuadletDir verifies that ValidateQuadlets
// invokes the generator with --user --dryrun.  The test places a fake
// podman-system-generator binary on PATH so the generator lookup succeeds.
func TestSystemd_ValidateQuadlets_UsesQuadletDir(t *testing.T) {
	binDir := t.TempDir()
	writeFakeBinary(t, binDir, "podman-system-generator")
	prependToPATH(t, binDir)

	c := NewClient(testLogger())
	quadletDir := t.TempDir()
	if err := c.ValidateQuadlets(context.Background(), quadletDir); err != nil {
		t.Fatalf("ValidateQuadlets: %v", err)
	}

	args := readCapturedArgs(binDir)
	if args == nil {
		t.Fatal("podman-system-generator was never called")
	}

	// Expect: --user --dryrun
	if len(args) < 2 {
		t.Fatalf("expected at least 2 args, got %v", args)
	}
	if args[0] != "--user" {
		t.Errorf("arg[0] = %q, want --user", args[0])
	}
	if args[1] != "--dryrun" {
		t.Errorf("arg[1] = %q, want --dryrun", args[1])
	}
}

// TestSystemd_GetUnitStatus_ParsesActive verifies that GetUnitStatus returns
// the trimmed stdout of the fake binary and does not surface a non-zero exit
// as an error (is-active exits non-zero for inactive units).
func TestSystemd_GetUnitStatus_ParsesActive(t *testing.T) {
	binDir := t.TempDir()

	// Write a fake systemctl that prints "active\n" and exits 0.
	script := "#!/bin/sh\necho active\nexit 0\n"
	p := filepath.Join(binDir, "systemctl")
	if err := os.WriteFile(p, []byte(script), 0755); err != nil {
		t.Fatalf("write fake systemctl: %v", err)
	}
	prependToPATH(t, binDir)

	c := NewClient(testLogger())
	status, err := c.GetUnitStatus(context.Background(), "app.service")
	if err != nil {
		t.Fatalf("GetUnitStatus returned unexpected error: %v", err)
	}
	if status != "active" {
		t.Errorf("status = %q, want %q", status, "active")
	}
}

// TestSystemd_GetUnitStatus_InactiveNoError verifies that GetUnitStatus does
// NOT return an error when the unit is inactive (is-active exits non-zero).
func TestSystemd_GetUnitStatus_InactiveNoError(t *testing.T) {
	binDir := t.TempDir()

	// Write a fake systemctl that prints "inactive\n" and exits 1.
	script := "#!/bin/sh\necho inactive\nexit 1\n"
	p := filepath.Join(binDir, "systemctl")
	if err := os.WriteFile(p, []byte(script), 0755); err != nil {
		t.Fatalf("write fake systemctl: %v", err)
	}
	prependToPATH(t, binDir)

	c := NewClient(testLogger())
	status, err := c.GetUnitStatus(context.Background(), "app.service")
	if err != nil {
		t.Errorf("GetUnitStatus must not return error for inactive unit: %v", err)
	}
	if status != "inactive" {
		t.Errorf("status = %q, want %q", status, "inactive")
	}
}

// TestSystemd_GetUnitStatus_MissingBinaryReturnsError verifies that
// GetUnitStatus propagates a non-ExitError (e.g. binary not found) instead of
// silently returning an empty status.
func TestSystemd_GetUnitStatus_MissingBinaryReturnsError(t *testing.T) {
	// Set PATH to an empty directory so systemctl cannot be found.
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	c := NewClient(testLogger())
	status, err := c.GetUnitStatus(context.Background(), "app.service")
	if err == nil {
		t.Fatal("GetUnitStatus should return an error when systemctl is not found")
	}
	if status != "" {
		t.Errorf("status should be empty on error, got %q", status)
	}
	if !strings.Contains(err.Error(), "systemctl is-active app.service") {
		t.Errorf("error should contain context about the command, got: %v", err)
	}
}
