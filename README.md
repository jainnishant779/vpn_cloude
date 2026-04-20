# QuickTunnel VNC

QuickTunnel VNC is a peer-to-peer VPN platform optimized for VNC remote desktop traffic. It combines a coordination control plane, regional relay fallback, and WireGuard-based encrypted tunnels between peers seamlessly.

## Key Features

- **ZeroTier-Style Networks**: Effortlessly join devices to a centrally managed network via a single CLI command or bash one-liner.
- **Admin Approval Workflow**: Devices joining a network wait in a `pending` state until an admin approves them directly from the web dashboard.
- **Encrypted Peer-to-Peer**: Direct device-to-device connectivity using WireGuard for secure and fast VNC or data access.
- **Relay Fallback**: Automatic UDP relay fallback when standard NAT traversal (hole punching) cannot establish a direct path.
- **VNC Auto-Discovery**: Automatically discovers locally running VNC servers and seamlessly proxies them over the encrypted tunnel.
- **Web Dashboard**: React + TypeScript administration panel for managing networks, viewing peers, and approving joining devices.
- **Cross-Platform**: Support for Linux, Windows, and macOS agents.

## Repository Layout

- `server/`: Go-based Control Plane API handling auth, networking lifecycle, coord APIs, and PostgreSQL database migrations.
- `relay/`: High-performance Go UDP relay for fallback data transport when P2P hole-punching fails.
- `client/`: Go agent runtime for NAT traversal, tunnel establishment, peer management, and VNC discovery.
- `web/`: React + TypeScript frontend dashboard for comprehensive network and access management.
- `pkg/`: Shared utility packages including protocol mapping, modern encryption, and network operations.
- `tests/e2e/`: End-to-end full network flow integration tests.

## Getting Started (Docker Compose)

The easiest way to get the entire QuickTunnel stack (Server, Relay, Postgres, Redis, Web Dashboard) running is via Docker Compose:

```bash
# 1. Configure environment variables
cp .env.example .env

# 2. Build and run the entire stack
docker compose up --build -d
```

Services exposed:
- Web Dashboard: `http://localhost:3000`
- API Server: `http://localhost:8080`
- Relay Health: `http://localhost:8081`
- Relay UDP: `3478/udp`

## Connecting Devices (Client)

Once your server is running and you have created a network via the dashboard (giving you a `Network ID` like `5agrlxob7exh`), you can connect client devices in multiple ways.

### Option 1: The One-Liner (Linux/macOS)
If you do not have the agent binary installed yet, you can use the curl installer which automatically downloads the correct binary and joins the network:

```bash
curl http://<server-ip>:8080/join/<network-id> | sudo bash
```

### Option 2: Using the CLI explicitly
If you already have the QuickTunnel binary downloaded and in your path:

```bash
quicktunnel join <server-ip>:8080 <network-id>
```

> **Note**: Both methods will place the device in a `pending` state. An admin must log in to the Web Dashboard and approve the device. Once approved, the agent will receive a Virtual IP and immediately establish encrypted tunnel connections to other peers in the network.

## CLI Commands Reference

- `quicktunnel join <server> <network_id>` - Join a network and wait for approval.
- `quicktunnel up` - Reconnect to the network using the saved configuration.
- `quicktunnel down` / `leave` - Disconnect from the active ZeroTier network.
- `quicktunnel status` - Show live active connection and peer status.
- `quicktunnel peers` - List all other approved and online peers in your network.
- `quicktunnel vnc <peer-name>` - Automatically open a VNC viewer connected over the secure tunnel to the specific peer.
- `quicktunnel config` - View or set manual configuration overrides.

## Local Development (Without Compose)

For backend/client developers who wish to run services locally outside of docker:

```bash
# Start required DB dependencies
docker compose up -d postgres redis

# Terminal 1: Run the API Server
cd server && go run ./cmd/server

# Terminal 2: Run the Relay Server
cd relay && go run ./cmd/relay

# Terminal 3: Run the Web Dashboard
cd web && npm install && npm run dev
```

## Available Documentation

Detailed implementation logic and design architecture can be found in the `docs/` directory:

- [Documentation Overview](DOCUMENTATION_OVERVIEW.md)
- [Architecture](docs/architecture.md)
- [API Reference](docs/api.md)
- [Protocol](docs/protocol.md)
- [Setup Guide](docs/setup.md)
- [Troubleshooting](docs/troubleshooting.md)
