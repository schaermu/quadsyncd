# Tier 2 E2E Test Container - Fedora with systemd
# Close-to-real environment with systemd, Podman, and Quadlet generator

# Builder stage: compile quadsyncd for Linux
FROM golang:1.26-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o quadsyncd ./cmd/quadsyncd

# Runtime stage: Fedora with systemd
FROM fedora:41

# Install required packages
RUN dnf install -y \
    systemd \
    dbus \
    git \
    podman \
    shadow-utils \
    # Future runtime dependencies for rootless Podman
    slirp4netns \
    fuse-overlayfs \
    && dnf clean all

# Copy quadsyncd binary
COPY --from=builder /build/quadsyncd /usr/local/bin/quadsyncd
RUN chmod +x /usr/local/bin/quadsyncd

# Create quadsync user (UID 1000)
RUN useradd -u 1000 -m -s /bin/bash quadsync

# Configure subuid/subgid for rootless Podman (future runtime tests)
RUN echo "quadsync:100000:65536" >> /etc/subuid && \
    echo "quadsync:100000:65536" >> /etc/subgid

# Tier 2 runs a full systemd-in-container environment. On some Docker hosts,
# starting the per-user manager via user@.service fails at the PAM step
# (commonly due to pam_loginuid/pam_namespace restrictions or AppArmor profiles
# around unix_chkpwd). For the E2E container we don't need PAM; we need a working
# `systemctl --user`.
#
# Disable PAM for user@.service and set XDG_RUNTIME_DIR explicitly.
RUN mkdir -p /etc/systemd/system/user@.service.d && \
    cat > /etc/systemd/system/user@.service.d/quadsyncd-e2e.conf <<'EOF'
[Service]
PAMName=
Environment=XDG_RUNTIME_DIR=/run/user/%i
EOF

# Systemd expects these
ENV container=docker
STOPSIGNAL SIGRTMIN+3

# Start systemd as PID 1
CMD ["/sbin/init"]
