# =============================================================================
# Multi-stage Docker build for Presence App
# Produces a minimal image with the Go binary + embedded templates/static files
# =============================================================================

# --- Stage 1: Build ---
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

# Copy go.mod first for dependency caching
COPY go.mod ./
RUN go mod download 2>/dev/null || true

# Copy all source
COPY . .

# Resolve dependencies (generates go.sum if needed)
RUN go mod tidy

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app .

# --- Stage 2: Runtime ---
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

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
