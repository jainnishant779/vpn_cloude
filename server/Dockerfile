FROM golang:1.21-alpine AS builder

WORKDIR /src
COPY . .

RUN go work sync \
  && cd server \
  && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.20

RUN adduser -D -u 10001 quicktunnel

WORKDIR /app
COPY --from=builder /out/server /usr/local/bin/server
COPY --from=builder /src/server/internal/database/migrations /app/internal/database/migrations

USER quicktunnel
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/server"]
