# QuickTunnel VNC

QuickTunnel VNC is a peer-to-peer VPN platform optimized for VNC remote desktop traffic.
It combines a coordination control plane, regional relay fallback, and WireGuard-based encrypted tunnels between peers.

## Highlights

- Encrypted peer-to-peer connectivity for device-to-device VNC access
- Relay fallback when NAT traversal cannot establish direct paths
- Coordination APIs for peer discovery, endpoint announce, and relay assignment
- Cross-platform client architecture (Linux, Windows, macOS)
- Web dashboard for network and peer operations

## Repository Layout

- `server/`: Control plane API, auth, DB access, coordination services
- `relay/`: UDP relay for fallback data transport
- `client/`: Agent runtime, NAT traversal, tunnel and peer management
- `web/`: React + TypeScript dashboard
- `pkg/`: Shared protocol, crypto, and net utilities
- `tests/e2e/`: End-to-end full-flow integration test harness
- `docs/`: Setup, architecture, API, protocol, and troubleshooting guides

## Quick Start

1. Configure environment variables:

   - Copy `.env.example` and customize values.

2. Start dependencies:

   - `docker compose up -d postgres redis`

3. Build server and relay:

   - `make build-server`
   - `make build-relay`

4. Start server:

   - `cd server && go run ./cmd/server`

5. Start relay:

   - `cd relay && go run ./cmd/relay`

6. Build and run client:

   - `cd client && go run ./cmd/quicktunnel up`

7. Run web dashboard:

   - `cd web && npm install && npm run dev`

## Easy Launcher

Use the root launcher to run core flows with a single command.

1. Start server (and postgres/redis dependencies):

   - `python main.py server`

2. Join a network with API key:

   - `python main.py join --network-id <network-id> --api-key <api-key>`

3. One-shot local stack (deps + server + relay + join):

   - `python main.py all --network-id <network-id> --api-key <api-key>`

Email login is also supported for `join` and `all`:

- `--email <email> --password <password>`

## Full Stack Compose

To build and run full stack (Postgres, Redis, server, relay, web):

- `docker compose up --build`

## Testing

- Unit tests (per module):
  - `cd server && go test ./...`
  - `cd relay && go test ./...`
  - `cd client && go test ./...`

- E2E flow test:
  - `go test -tags e2e ./tests/e2e/...`

## Documentation

- [Documentation Overview](DOCUMENTATION_OVERVIEW.md)
- [Setup Guide](docs/setup.md)
- [Architecture](docs/architecture.md)
- [API Reference](docs/api.md)
- [Protocol](docs/protocol.md)
- [Troubleshooting](docs/troubleshooting.md)
