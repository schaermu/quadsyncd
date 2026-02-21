# Deployment Guide

This guide covers deploying quadsyncd to sync Podman Quadlets on a rootless Podman-enabled server using systemd user services.

## Prerequisites

- Podman installed and configured for rootless operation
- Systemd user session accessible
- Git installed
- SSH key or GitHub token for repository access
- quadsyncd installed — see [[Installation]]

## Timer-Based Sync (systemd)

The default deployment mode runs `quadsyncd sync` periodically via a systemd user timer.

### 1. Install Systemd User Units

Copy the systemd units from the release archive:

```bash
mkdir -p ~/.config/systemd/user
cp packaging/systemd/user/quadsyncd-sync.service ~/.config/systemd/user/
cp packaging/systemd/user/quadsyncd-sync.timer ~/.config/systemd/user/

# Reload systemd to pick up new units
systemctl --user daemon-reload
```

### 2. Enable Lingering (Required)

Enable lingering so the timer runs even when you're not logged in:

```bash
loginctl enable-linger $USER
```

Verify lingering is enabled:

```bash
loginctl show-user $USER | grep Linger
# Should show: Linger=yes
```

### 3. Start and Enable the Timer

```bash
# Enable timer to start on boot
systemctl --user enable quadsyncd-sync.timer

# Start timer immediately
systemctl --user start quadsyncd-sync.timer

# Check timer status
systemctl --user status quadsyncd-sync.timer

# List next scheduled runs
systemctl --user list-timers quadsyncd-sync.timer
```

**Frequency**: The default timer runs every 5 minutes with 30 seconds of random jitter (see `packaging/systemd/user/quadsyncd-sync.timer`).

## Key Paths

| Path | Description |
|------|-------------|
| `~/.config/containers/systemd/` | Quadlet files synced by quadsyncd |
| `~/.local/state/quadsyncd/state.json` | State tracking file |
| `~/.local/state/quadsyncd/repo/` | Git repository checkout |
| `~/.config/quadsyncd/config.yaml` | Configuration file |
| `~/.local/bin/quadsyncd` | Binary location |
| `~/.config/systemd/user/` | Systemd user units |

## Uninstallation

```bash
# Stop and disable timer
systemctl --user stop quadsyncd-sync.timer
systemctl --user disable quadsyncd-sync.timer

# Remove units
rm ~/.config/systemd/user/quadsyncd-sync.*
systemctl --user daemon-reload

# Remove binary and config
rm ~/.local/bin/quadsyncd
rm -rf ~/.config/quadsyncd
rm -rf ~/.local/state/quadsyncd
```

## Next Steps

- Configure webhook mode for real-time syncing — see [[Webhook Setup]]
- Check the [[Troubleshooting]] page if you encounter issues
