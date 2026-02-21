# Security Policy

## Supported Versions

quadsyncd is currently in early development. Security updates will be provided for the latest release only.

| Version | Supported          |
| ------- | ------------------ |
| Latest  | :white_check_mark: |
| < Latest| :x:                |

Once version 1.0 is released, we will establish a more formal support policy.

## Security Considerations

### Running Rootless

quadsyncd is designed to run entirely in userspace without root privileges. This is a fundamental security principle:

- All operations use `systemctl --user` (never root systemd)
- Files are managed in user directories (`~/.config/`, `~/.local/`)
- No privileged system access is required or requested

### Secrets Management

**Never commit secrets to the repository or configuration files.**

quadsyncd supports secure secret handling via file references:

- `auth.ssh_key_file`: Path to SSH private key
- `auth.https_token_file`: Path to file containing GitHub token
- `serve.github_webhook_secret_file`: Path to webhook secret

All secret files should have restrictive permissions:

```bash
chmod 600 ~/.ssh/quadsyncd_deploy_key
chmod 600 ~/.config/quadsyncd/github_token
chmod 600 ~/.config/quadsyncd/webhook_secret
```

### Repository Authentication

When using SSH deploy keys:

- Generate dedicated keys for quadsyncd (do not reuse personal SSH keys)
- Use read-only deploy keys when possible (no write access needed)
- Rotate keys periodically

When using HTTPS tokens:

- Use fine-grained personal access tokens with minimal scope
- Limit token to specific repositories
- Set expiration dates and rotate before expiry

### Webhook Security

Webhook mode (`quadsyncd serve`) includes the following security measures:

- HMAC-SHA256 signature verification (`X-Hub-Signature-256`)
- Localhost-only binding (`127.0.0.1`) with reverse proxy/tunnel
- Ref filtering (only allowed branches trigger syncs)
- Request size limits and timeouts
- Debouncing and single-flight execution

See the [Webhook Setup](https://github.com/schaermu/quadsyncd/wiki/Webhook-Setup) wiki page for deployment architecture.

### Supply Chain Security

quadsyncd follows these practices:

- Dependencies are tracked in `go.mod` with checksums in `go.sum`
- `govulncheck` runs in CI on every commit
- Releases are built via GitHub Actions with provenance
- All binaries are statically linked (no dynamic dependencies)

## Reporting a Vulnerability

If you discover a security vulnerability in quadsyncd, please report it responsibly:

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please report security issues via email to:

**security@schaermich.ch**

Please include:

- Description of the vulnerability
- Steps to reproduce (if applicable)
- Potential impact
- Suggested fix (if you have one)

### Response Timeline

- We will acknowledge receipt within 48 hours
- We will provide an initial assessment within 7 days
- We will work with you to understand and address the issue
- We will coordinate disclosure timing with you

### After a Fix

Once a security issue is fixed:

1. A new release will be published with the fix
2. A security advisory will be published on GitHub
3. Credit will be given to the reporter (unless anonymity is requested)

## Security Best Practices for Users

When deploying quadsyncd:

1. **Use dedicated credentials**: Create separate deploy keys/tokens for quadsyncd
2. **Limit scope**: Use read-only access when possible
3. **Enable lingering carefully**: `loginctl enable-linger` allows processes to run without login; ensure your system is properly secured
4. **Monitor logs**: Regularly check `journalctl --user -u quadsyncd-sync.service` for unexpected activity
5. **Review sync targets**: Ensure your `repo.subdir` and `repo.ref` settings point to trusted, controlled sources
6. **Audit quadlet files**: Review generated files in `~/.config/containers/systemd/` before containers start

## Security Assumptions

quadsyncd's threat model assumes:

- The Git repository containing quadlet files is trusted and access-controlled
- The server running quadsyncd is reasonably secured (not compromised)
- SSH keys/tokens are stored with appropriate filesystem permissions
- The systemd user session is isolated from other users on multi-user systems

quadsyncd does not protect against:

- Malicious quadlet files in a compromised repository (garbage in, garbage out)
- Compromised server with root access (rootless mode protects the host, not the user session)
- Social engineering attacks targeting repository access

## License

This security policy is part of quadsyncd and is covered by the MIT License.
