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
