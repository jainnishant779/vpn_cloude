FROM golang:1.21-alpine AS builder

WORKDIR /src
COPY go.work go.work.sum ./
COPY client ./client
COPY pkg ./pkg
COPY relay ./relay
COPY server ./server

RUN set -eux; \
  go work sync; \
  mkdir -p /out/client; \
  cd /src/server; \
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server; \
  cd /src/client; \
  build_client() { \
    goos="$1"; \
    goarch="$2"; \
    goarm="$3"; \
    output="$4"; \
    if [ -n "$goarm" ]; then \
      CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" go build -trimpath -ldflags="-s -w" -o "$output" ./cmd/quicktunnel; \
      return; \
    fi; \
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags="-s -w" -o "$output" ./cmd/quicktunnel; \
  }; \
  build_client linux amd64 "" /out/client/quicktunnel-linux-amd64; \
  build_client linux arm64 "" /out/client/quicktunnel-linux-arm64; \
  build_client linux arm 7 /out/client/quicktunnel-linux-armv7; \
  build_client darwin amd64 "" /out/client/quicktunnel-darwin-amd64; \
  build_client darwin arm64 "" /out/client/quicktunnel-darwin-arm64; \
  build_client windows amd64 "" /out/client/quicktunnel-windows-amd64.exe

FROM alpine:3.20

RUN adduser -D -u 10001 quicktunnel

WORKDIR /app
COPY --from=builder /out/server /usr/local/bin/server

# Copy prebuilt client binaries for the install/download endpoint.
COPY --from=builder /out/client /app/client/bin
COPY --from=builder /src/server/internal/database/migrations /app/internal/database/migrations

USER quicktunnel
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/server"]
