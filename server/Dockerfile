# syntax=docker/dockerfile:1
FROM golang:1.21-alpine AS builder

WORKDIR /src

# Cache dependencies separately (only re-runs when go.work/go.sum changes)
COPY go.work go.work.sum ./
COPY client/go.mod client/go.mod
COPY pkg/go.mod pkg/go.mod
COPY relay/go.mod relay/go.mod
COPY server/go.mod server/go.mod

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go work sync

# Copy source and build
COPY client ./client
COPY pkg    ./pkg
COPY relay  ./relay
COPY server ./server

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd server && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ── Final image ──────────────────────────────────────────────────────────────
FROM alpine:3.20

RUN adduser -D -u 10001 quicktunnel

WORKDIR /app
COPY --from=builder /out/server /usr/local/bin/server
COPY --from=builder /src/server/internal/database/migrations /app/internal/database/migrations

USER quicktunnel
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server"]
