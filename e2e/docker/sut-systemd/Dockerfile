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
    uidmap \
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

# Systemd expects these
ENV container=docker
STOPSIGNAL SIGRTMIN+3

# Start systemd as PID 1
CMD ["/sbin/init"]
