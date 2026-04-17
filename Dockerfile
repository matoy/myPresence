# =============================================================================
# Multi-stage Docker build for Presence App
# Produces a minimal image with the Go binary + embedded templates/static files
# =============================================================================

# --- Stage 1: Build ---
FROM golang:1.25.9-alpine AS builder


WORKDIR /build
ENV GONOSUMDB=*
ENV GOFLAGS=-mod=vendor

# Copy all source (vendor included)
COPY . .

# Build static binary using vendored dependencies (no network access needed)
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -ldflags="-s -w" -o /app .

# --- Stage 2: Runtime ---
FROM alpine:3.22


# Create non-root user
RUN addgroup -g 1001 -S appuser && \
    adduser -u 1001 -S appuser -G appuser

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app /app/presence

# Create data directory
RUN mkdir -p /data && chown appuser:appuser /data

# Switch to non-root user
USER appuser

EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s \
    CMD wget -qO- http://localhost:8080/login || exit 1

ENTRYPOINT ["/app/presence"]
