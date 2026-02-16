# Deploying quadsyncd with Rootless Systemd

This guide covers deploying quadsyncd to sync Podman Quadlets on a rootless Podman-enabled server.

## Prerequisites

- Podman installed and configured for rootless operation
- Systemd user session accessible
- Git installed
- SSH key or GitHub token for repository access

## Installation Steps

### 1. Download and Install Binary

Download the latest release from GitHub:

```bash
# Download for your platform (example for linux/amd64)
wget https://github.com/schaermu/quadsyncd/releases/latest/download/quadsyncd_<version>_Linux_x86_64.tar.gz

# Extract
tar xzf quadsyncd_<version>_Linux_x86_64.tar.gz

# Install binary
mkdir -p ~/.local/bin
cp quadsyncd ~/.local/bin/
chmod +x ~/.local/bin/quadsyncd

# Ensure ~/.local/bin is in PATH
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

Verify installation:

```bash
quadsyncd version
```

### 2. Create Configuration

Create the config directory and file:

```bash
mkdir -p ~/.config/quadsyncd
cp config.example.yaml ~/.config/quadsyncd/config.yaml
```

Edit `~/.config/quadsyncd/config.yaml` with your repository details:

```yaml
repo:
  url: "git@github.com:your-org/your-quadlets-repo.git"
  ref: "refs/heads/main"
  subdir: "quadlets"  # subdirectory in repo containing .container files

paths:
  quadlet_dir: "${HOME}/.config/containers/systemd"
  state_dir: "${HOME}/.local/state/quadsyncd"

sync:
  prune: true
  restart: "changed"

auth:
  ssh_key_file: "${HOME}/.ssh/quadsyncd_deploy_key"
```

### 3. Set Up Authentication

#### Option A: SSH Deploy Key (Recommended)

Generate a dedicated SSH key:

```bash
ssh-keygen -t ed25519 -f ~/.ssh/quadsyncd_deploy_key -N ""
```

Add the public key (`~/.ssh/quadsyncd_deploy_key.pub`) as a deploy key in your GitHub repository (Settings → Deploy Keys).

#### Option B: HTTPS with Personal Access Token

Create a token file:

```bash
echo "your_github_token" > ~/.config/quadsyncd/github_token
chmod 600 ~/.config/quadsyncd/github_token
```

Update config.yaml to use HTTPS:

```yaml
repo:
  url: "https://github.com/your-org/your-quadlets-repo.git"

auth:
  https_token_file: "${HOME}/.config/quadsyncd/github_token"
```

### 4. Test Manual Sync

Before setting up the timer, test a manual sync:

```bash
quadsyncd sync --dry-run
```

If everything looks correct, run without dry-run:

```bash
quadsyncd sync
```

### 5. Install Systemd User Units

Copy the systemd units from the release archive:

```bash
mkdir -p ~/.config/systemd/user
cp packaging/systemd/user/quadsyncd-sync.service ~/.config/systemd/user/
cp packaging/systemd/user/quadsyncd-sync.timer ~/.config/systemd/user/

# Reload systemd to pick up new units
systemctl --user daemon-reload
```

### 6. Enable Lingering (Required)

Enable lingering so the timer runs even when you're not logged in:

```bash
loginctl enable-linger $USER
```

Verify lingering is enabled:

```bash
loginctl show-user $USER | grep Linger
# Should show: Linger=yes
```

### 7. Start and Enable the Timer

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

## Configuration Options

### Restart Policies

The `sync.restart` option controls what happens after sync:

- `none`: Only reload systemd daemon, don't restart any units
- `changed`: Restart only units whose quadlet files changed (recommended)
- `all-managed`: Restart all units managed by quadsyncd (more disruptive)

### Pruning

When `sync.prune` is `true`, quadlet files that were previously synced but no longer exist in the repository will be deleted. Set to `false` if you want to manually manage file removal.

## Troubleshooting

### Check Sync Logs

```bash
# View recent sync logs
journalctl --user -u quadsyncd-sync.service -n 50

# Follow logs in real-time
journalctl --user -u quadsyncd-sync.service -f
```

### Verify Systemd User Session

```bash
systemctl --user status
```

If this fails, your user session may not be properly configured for rootless systemd.

### Check Quadlet Directory

```bash
ls -la ~/.config/containers/systemd/
```

### Verify Podman Generated Units

After a sync, Podman's systemd generator should create service files:

```bash
systemctl --user list-units | grep 'your-container-name'
```

### Debug Authentication Issues

Test git access manually:

```bash
GIT_SSH_COMMAND="ssh -i ~/.ssh/quadsyncd_deploy_key" git ls-remote git@github.com:your-org/your-repo.git
```

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

- See docs/webhook-reverse-proxy.md for webhook mode (future)
- Review AGENTS.md for development/contribution guidelines
