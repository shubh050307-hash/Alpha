# Dockerfile
# Alpha Network — alphanode
# Multi-stage build: compile in Go builder, run in minimal Debian slim
# Final image: ~50MB, no build tools, no source code

# ─── Stage 1: Build ───────────────────────────────────────────────────────────
FROM golang:1.25-bookworm AS builder

WORKDIR /build

# Copy dependency files first (layer cache — only re-downloads if go.mod changes)
COPY go.mod go.sum ./
RUN go mod download

# Copy full source
COPY . .

# Build the main node binary
# CGO_ENABLED=0 → fully static binary, no libc dependency
# -trimpath → removes local build paths from binary
# -ldflags "-s -w" → strips debug symbols, reduces binary size
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /build/alphanode \
    .

# ─── Stage 2: Runtime ─────────────────────────────────────────────────────────
FROM debian:bookworm-slim

# Install ca-certificates (needed for HTTPS calls) and curl (healthcheck)
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user for security
RUN useradd -r -u 1001 -m -d /home/alphanode alphanode

# Copy binary from builder
COPY --from=builder /build/alphanode /usr/local/bin/alphanode

# Copy genesis file (required at startup)
COPY genesis.json /etc/alphanode/genesis.json

# Data directory — mount a volume here for persistence
# Without a volume, chain data is lost on container restart
RUN mkdir -p /var/lib/alphanode/data \
             /var/lib/alphanode/intelligence \
             /var/lib/alphanode/ipfs \
    && chown -R alphanode:alphanode /var/lib/alphanode

# Ports:
#   8080 — HTTP API (REST + chain RPC)
#   8081 — WebSocket (live block/event stream)
EXPOSE 8080 8081

# Run as non-root
USER alphanode

# Healthcheck — hits the stats endpoint every 30s
HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
    CMD curl -f http://localhost:8080/api/v1/intelligence/stats || exit 1

# Default entrypoint
ENTRYPOINT ["/usr/local/bin/alphanode"]

# Default flags — all overridable at runtime via docker run args
CMD ["-datadir=/var/lib/alphanode", "-port=8080", "-ws-port=8081"]
