# Documentation Overview

This file provides a quick map of major architecture and implementation artifacts.

## Core Architecture

- Control Plane: `server/`
  - Authentication (JWT + API key middleware)
  - Network and peer lifecycle APIs
  - Coordination endpoints with Redis caching
  - PostgreSQL migrations and typed query stores

- Relay Plane: `relay/`
  - UDP packet relay with session pairing
  - Session cleanup and health endpoint
  - Metrics snapshot and periodic reporting hooks

- Data Plane Client: `client/`
  - API client with retries and auth modes
  - NAT traversal (STUN detection + hole punching)
  - Cross-platform TUN abstraction and tunnel manager
  - Peer manager with P2P-first, relay fallback policy
  - Agent lifecycle state machine and CLI commands

- Dashboard: `web/`
  - React + TypeScript routes and protected layout
  - Auth and network stores (Zustand)
  - Network and peer management UI pages

## Operational Artifacts

- Root compose stack: `docker-compose.yml`
- Runtime env template: `.env.example`
- Build scripts: `scripts/build.sh`, `scripts/install.sh`
- Docker images:
  - `server/Dockerfile`
  - `relay/Dockerfile`
  - `web/Dockerfile`

## Test Artifacts

- Server tests under `server/internal/.../*_test.go`
- Relay tests under `relay/internal/relay_server_test.go`
- End-to-end flow harness: `tests/e2e/full_flow_test.go`

## Detailed Docs

- `docs/setup.md`
- `docs/architecture.md`
- `docs/api.md`
- `docs/protocol.md`
- `docs/troubleshooting.md`
