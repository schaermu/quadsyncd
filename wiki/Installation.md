# Installation

## Prerequisites

- Podman configured for rootless operation
- Systemd user session
- Git installed
- SSH key or GitHub token for repository access

## Download and Install Binary

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

## Create Configuration

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

See the [[Configuration]] page for a complete reference of all options.

## Set Up Authentication

### Option A: SSH Deploy Key (Recommended)

Generate a dedicated SSH key:

```bash
ssh-keygen -t ed25519 -f ~/.ssh/quadsyncd_deploy_key -N ""
```

Add the public key (`~/.ssh/quadsyncd_deploy_key.pub`) as a deploy key in your GitHub repository (Settings → Deploy Keys).

### Option B: HTTPS with Personal Access Token

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

## Test Manual Sync

Before setting up the timer, test a manual sync:

```bash
quadsyncd sync --dry-run
```

If everything looks correct, run without dry-run:

```bash
quadsyncd sync
```

## Next Steps

- Set up automatic syncing via systemd timer — see [[Deployment Guide]]
- Configure webhook-based syncing for real-time updates — see [[Webhook Setup]]
