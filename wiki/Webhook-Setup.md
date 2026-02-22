# Webhook Setup

This guide describes how to configure webhook mode (`quadsyncd serve`) for real-time sync using a reverse proxy or tunnel service.

## Architecture

```
GitHub Webhook → Reverse Proxy/Tunnel → quadsyncd serve (127.0.0.1:8787)
                                              ↓
                                         Sync Engine
                                              ↓
                                    ~/.config/containers/systemd/
```

## Configuration

Enable webhook mode in `~/.config/quadsyncd/config.yaml`:

```yaml
serve:
  enabled: true
  listen_addr: "127.0.0.1:8787"
  github_webhook_secret_file: "${HOME}/.config/quadsyncd/webhook_secret"
  allowed_event_types: ["push"]
  allowed_refs: ["refs/heads/main"]
```

Create a webhook secret:

```bash
openssl rand -hex 32 > ~/.config/quadsyncd/webhook_secret
chmod 600 ~/.config/quadsyncd/webhook_secret
```

## Reverse Proxy Options

### Option 1: Caddy (Recommended)

Install Caddy and create `/etc/caddy/Caddyfile`:

```
webhooks.yourdomain.com {
    reverse_proxy 127.0.0.1:8787
}
```

Restart Caddy:

```bash
sudo systemctl restart caddy
```

### Option 2: Nginx

```nginx
server {
    listen 443 ssl http2;
    server_name webhooks.yourdomain.com;

    ssl_certificate /etc/letsencrypt/live/yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/yourdomain.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8787;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

### Option 3: Cloudflare Tunnel (No Public IP Required)

Install cloudflared:

```bash
# Download cloudflared
wget https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64
sudo mv cloudflared-linux-amd64 /usr/local/bin/cloudflared
sudo chmod +x /usr/local/bin/cloudflared

# Authenticate
cloudflared tunnel login

# Create tunnel
cloudflared tunnel create quadsyncd-webhooks

# Configure tunnel
cat > ~/.cloudflared/config.yml <<EOF
tunnel: <tunnel-id>
credentials-file: /home/$USER/.cloudflared/<tunnel-id>.json

ingress:
  - hostname: webhooks.yourdomain.com
    service: http://127.0.0.1:8787
  - service: http_status:404
EOF

# Run tunnel
cloudflared tunnel run quadsyncd-webhooks
```

## Start Webhook Service

### Option A: Socket Activation (Recommended)

Socket activation starts the webhook service on-demand when the first request arrives:

```bash
# Install webhook service and socket units
cp packaging/systemd/user/quadsyncd-webhook.service ~/.config/systemd/user/
cp packaging/systemd/user/quadsyncd-webhook.socket ~/.config/systemd/user/
systemctl --user daemon-reload

# Enable and start the socket (not the service)
systemctl --user enable --now quadsyncd-webhook.socket

# Check socket status
systemctl --user status quadsyncd-webhook.socket

# Verify the service is not running yet
systemctl --user status quadsyncd-webhook.service
```

**Benefits:**
- Service starts only when needed (first webhook request)
- Automatic restart if service crashes (socket remains active)
- Lower resource usage when idle
- Faster boot times

**How it works:**
- systemd listens on port 8787
- First webhook triggers service start
- quadsyncd receives the socket via LISTEN_FDS
- Service stops when idle (can be configured with `RuntimeMaxSec`)

> **Note:** In socket activation mode (Option A), the listen address and port are configured in the `quadsyncd-webhook.socket` unit via its `ListenStream` directive. The `serve.listen_addr` setting in `~/.config/quadsyncd/config.yaml` is only used when running in always-running mode (Option B).
### Option B: Always-Running Service

Traditional mode where the service runs continuously:

```bash
# Install webhook service unit
cp packaging/systemd/user/quadsyncd-webhook.service ~/.config/systemd/user/
systemctl --user daemon-reload

# Enable and start
systemctl --user enable --now quadsyncd-webhook.service

# Check status
systemctl --user status quadsyncd-webhook.service
```

## Configure GitHub Webhook

1. Go to your repository Settings → Webhooks → Add webhook
2. Payload URL: `https://webhooks.yourdomain.com`
3. Content type: `application/json`
4. Secret: (paste content of `~/.config/quadsyncd/webhook_secret`)
5. Events: Select "Just the push event"
6. Active: checked

## Testing

Send a test event from GitHub webhook settings, then check logs:

```bash
journalctl --user -u quadsyncd-webhook.service -f
```

## Security Considerations

- Always bind `quadsyncd serve` to `127.0.0.1` (localhost)
- Use HTTPS on the reverse proxy/tunnel
- Configure webhook secret verification
- Use `allowed_refs` to restrict which branches trigger syncs
- Consider firewall rules to restrict proxy access

## Troubleshooting

### Webhook Not Triggering

Check GitHub webhook delivery logs (Settings → Webhooks → Recent Deliveries).

### Signature Verification Failures

Ensure the secret in GitHub matches `webhook_secret` file exactly (no trailing newline).

### Service Crashes

```bash
journalctl --user -u quadsyncd-webhook.service --since "5 minutes ago"
```

### Socket Activation Issues

**Service doesn't start on first request:**

```bash
# Check socket is active
systemctl --user status quadsyncd-webhook.socket

# Check socket is listening
ss -tlnp | grep 8787

# View socket logs
journalctl --user -u quadsyncd-webhook.socket
```

**Multiple listeners error:**

If you see "received N socket-activated listeners, expected exactly 1":

```bash
# Edit the socket unit to have only one ListenStream directive
systemctl --user edit quadsyncd-webhook.socket
```

**Verifying socket activation mode:**

Check logs to confirm which mode is being used:

```bash
# Socket activation mode shows:
# "using systemd socket activation" mode=socket-activated

# Normal bind mode shows:
# "webhook server bound to address" mode=bind
journalctl --user -u quadsyncd-webhook.service -n 20
```
