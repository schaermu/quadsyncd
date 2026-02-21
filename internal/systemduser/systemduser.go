package systemduser

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// Systemd provides operations for interacting with systemd user units
type Systemd interface {
	// DaemonReload reloads systemd user configuration
	DaemonReload(ctx context.Context) error
	// TryRestartUnits attempts to restart the specified units
	TryRestartUnits(ctx context.Context, units []string) error
	// IsAvailable checks if systemctl --user is accessible
	IsAvailable(ctx context.Context) (bool, error)
	// ValidateQuadlets runs the podman quadlet generator in dry-run mode to
	// validate that the quadlet files can be converted into systemd units.
	// quadletDir is the directory containing the quadlet files to validate.
	ValidateQuadlets(ctx context.Context, quadletDir string) error
}

// Client implements Systemd by shelling out to systemctl --user
type Client struct{}

// NewClient creates a new systemd client
func NewClient() *Client {
	return &Client{}
}

// DaemonReload reloads systemd user daemon configuration
func (c *Client) DaemonReload(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %w: %s", err, string(output))
	}
	return nil
}

// TryRestartUnits attempts to restart the specified units
// Uses try-restart to avoid errors if units don't exist or aren't running
func (c *Client) TryRestartUnits(ctx context.Context, units []string) error {
	if len(units) == 0 {
		return nil
	}

	args := append([]string{"--user", "try-restart"}, units...)
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// try-restart can fail for various non-critical reasons
		// Log but don't fail the entire sync
		return fmt.Errorf("systemctl try-restart had issues (may be non-fatal): %w: %s", err, string(output))
	}
	return nil
}

// IsAvailable checks if systemctl --user is accessible
func (c *Client) IsAvailable(ctx context.Context) (bool, error) {
	cmd := exec.CommandContext(ctx, "systemctl", "--user", "status")
	err := cmd.Run()

	// systemctl status returns non-zero for degraded systems, but it's still available
	// We only care if the command can run at all
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Exit codes 1-3 are normal for systemctl status
			if exitErr.ExitCode() <= 3 {
				return true, nil
			}
		}
		return false, fmt.Errorf("systemctl --user not available: %w", err)
	}

	return true, nil
}

// podmanSystemGeneratorFallback is the traditional hard-coded path for the
// Podman quadlet system generator binary.
const podmanSystemGeneratorFallback = "/usr/lib/systemd/system-generators/podman-system-generator"

// quadletGeneratorPath resolves the podman quadlet generator binary path.
// It prefers resolving via PATH and falls back to the traditional systemd
// generator location to preserve existing behavior.
func (c *Client) quadletGeneratorPath() string {
	if path, err := exec.LookPath("podman-system-generator"); err == nil {
		return path
	}
	return podmanSystemGeneratorFallback
}

// ValidateQuadlets runs the podman quadlet generator in dry-run mode to
// validate that the quadlet files in quadletDir can be converted into systemd
// units. If the generator binary is not present, validation is skipped with a
// warning. It reports any generator errors in the returned error.
func (c *Client) ValidateQuadlets(ctx context.Context, quadletDir string) error {
	generatorPath := c.quadletGeneratorPath()
	if _, err := os.Stat(generatorPath); err != nil {
		slog.Warn("podman-system-generator not found, skipping quadlet validation",
			"path", generatorPath,
			"quadlet_dir", quadletDir)
		return nil
	}
	cmd := exec.CommandContext(ctx, generatorPath, "--user", "--dryrun")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("podman-system-generator --dryrun (path %s): %w: %s", generatorPath, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// RestartUnits restarts the specified units (harder than try-restart)
func (c *Client) RestartUnits(ctx context.Context, units []string) error {
	if len(units) == 0 {
		return nil
	}

	args := append([]string{"--user", "restart"}, units...)
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart failed: %w: %s", err, string(output))
	}
	return nil
}

// GetUnitStatus returns the status of a unit
func (c *Client) GetUnitStatus(ctx context.Context, unit string) (string, error) {
	cmd := exec.CommandContext(ctx, "systemctl", "--user", "is-active", unit)
	output, err := cmd.Output()
	status := strings.TrimSpace(string(output))

	if err != nil {
		// is-active returns non-zero for inactive units, but that's not an error
		return status, nil
	}

	return status, nil
}
