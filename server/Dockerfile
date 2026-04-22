
# syntax=docker/dockerfile:1
FROM golang:1.22-alpine AS builder
WORKDIR /src

# Cache go.mod files for all modules (enables build cache)
COPY go.work go.work.sum ./
COPY server/go.mod server/go.mod
COPY client/go.mod client/go.mod
COPY pkg/go.mod pkg/go.mod
COPY relay/go.mod relay/go.mod

RUN --mount=type=cache,target=/go/pkg/mod \
  go work sync

# Copy full source only after deps
COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
  cd server && \
  CGO_ENABLED=0 go build -o /out/server ./cmd/server

# Build client binaries for all platforms
RUN --mount=type=cache,target=/go/pkg/mod \
  mkdir -p /out/client/bin && \
  cd client && \
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -o /out/client/bin/quicktunnel-linux-amd64 ./cmd/quicktunnel && \
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -buildvcs=false -trimpath -o /out/client/bin/quicktunnel-linux-arm64 ./cmd/quicktunnel && \
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -trimpath -o /out/client/bin/quicktunnel-windows-amd64.exe ./cmd/quicktunnel

# Final minimal image
FROM alpine:3.20
RUN adduser -D app
WORKDIR /app
COPY --from=builder /out/server /usr/local/bin/server
<<<<<<< HEAD
=======
COPY --from=builder /out/client/bin/ /app/client/bin/
>>>>>>> e01a61abb751904c995e57cef8c3d833518430b5
COPY --from=builder /src/server/internal/database/migrations ./internal/database/migrations
USER app
EXPOSE 8080
CMD ["server"]
