# =============================================================================
# Multi-stage Docker build for Presence App
# Produces a minimal image with the Go binary + embedded templates/static files
# =============================================================================

# --- Stage 1: Build ---
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

# Bypass corporate proxy TLS issues
ENV GONOSUMDB=*
ENV GOINSECURE=*
ENV GOPRIVATE=*
ENV GOPROXY=direct
ENV GIT_SSL_NO_VERIFY=true

# Copy go.mod first for dependency caching
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true

# Copy all source
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -mod=mod -ldflags="-s -w" -o /app .

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
