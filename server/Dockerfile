
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

# Final minimal image
FROM alpine:3.20
RUN adduser -D app
WORKDIR /app
COPY --from=builder /out/server /usr/local/bin/server
USER app
EXPOSE 8080
CMD ["server"]
