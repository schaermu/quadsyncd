# Troubleshooting

## Check Sync Logs

```bash
# View recent sync logs
journalctl --user -u quadsyncd-sync.service -n 50

# Follow logs in real-time
journalctl --user -u quadsyncd-sync.service -f
```

## Verify Systemd User Session

```bash
systemctl --user status
```

If this fails, your user session may not be properly configured for rootless systemd.

## Debug with Verbose Logging

Run with debug logging:

```bash
quadsyncd sync --log-level debug --config ~/.config/quadsyncd/config.yaml
```

## Authentication Issues

### SSH

Test git access manually:

```bash
GIT_SSH_COMMAND="ssh -i ~/.ssh/quadsyncd_deploy_key" git ls-remote git@github.com:your-org/your-repo.git
```

### HTTPS

Verify the token file exists and has correct permissions:

```bash
ls -la ~/.config/quadsyncd/github_token
# Should show: -rw------- (600)
```

## Inspect State

View the current sync state:

```bash
cat ~/.local/state/quadsyncd/state.json
```

## Check Quadlet Directory

Verify files were synced:

```bash
ls -la ~/.config/containers/systemd/
```

## Verify Podman Generated Units

After a sync, Podman's systemd generator should create service files:

```bash
systemctl --user list-units | grep 'your-container-name'
```

## Lingering Not Enabled

If the timer doesn't run when you're not logged in:

```bash
loginctl show-user $USER | grep Linger
# Should show: Linger=yes

# Enable if needed:
loginctl enable-linger $USER
```

## Webhook Troubleshooting

### Webhook Not Triggering

Check GitHub webhook delivery logs (Settings → Webhooks → Recent Deliveries).

### Signature Verification Failures

Ensure the secret in GitHub matches `webhook_secret` file exactly (no trailing newline):

```bash
cat -A ~/.config/quadsyncd/webhook_secret
```

### Webhook Service Issues

```bash
# Check service status
systemctl --user status quadsyncd-webhook.service

# View recent logs
journalctl --user -u quadsyncd-webhook.service --since "5 minutes ago"
```
