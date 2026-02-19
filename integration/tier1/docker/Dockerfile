# Tier 1 Integration Test Container
# Lightweight container with git, quadsyncd binary, and systemctl shim

# Builder stage: compile quadsyncd for Linux
FROM golang:1.26-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o quadsyncd ./cmd/quadsyncd

# Runtime stage: Alpine with git
FROM alpine:3.20
RUN apk add --no-cache git

# Copy quadsyncd binary
COPY --from=builder /build/quadsyncd /usr/local/bin/quadsyncd

# Install systemctl shim (will be ahead of real systemctl in PATH)
COPY integration/tier1/docker/systemctl-shim.sh /usr/local/bin/systemctl
RUN chmod +x /usr/local/bin/systemctl

# Create directories for testing
RUN mkdir -p /test/repo /test/config /test/quadlet /test/state

# Default command (tests will exec commands as needed)
CMD ["/bin/sh", "-c", "sleep infinity"]
