FROM golang:1.21-alpine AS builder

WORKDIR /src
COPY . .

RUN go work sync \
  && cd relay \
  && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/relay ./cmd/relay

FROM alpine:3.20

RUN adduser -D -u 10001 quicktunnel

WORKDIR /app
COPY --from=builder /out/relay /usr/local/bin/relay

USER quicktunnel
EXPOSE 3478/udp 8081

ENTRYPOINT ["/usr/local/bin/relay"]
