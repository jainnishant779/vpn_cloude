# Architecture

## Planes

### Control Plane

Responsibilities:

- User auth and JWT issuance
- Network CRUD and ownership checks
- Peer register and heartbeat tracking
- Coordination announce and endpoint discovery
- Relay assignment and health-aware selection

Main components:

- `server/internal/auth`
- `server/internal/api`
- `server/internal/database`
- PostgreSQL + Redis

### Relay Plane

Responsibilities:

- Pair peers into relay sessions via session token
- Forward UDP packets between paired peers
- Cleanup stale sessions and expose health status

Main components:

- `relay/internal/relay_server.go`
- `relay/internal/metrics.go`

### Data Plane (Client)

Responsibilities:

- Register machine and maintain heartbeat
- Discover public endpoint via STUN
- Attempt UDP hole punching for direct P2P
- Fall back to relay when direct path fails
- Maintain tunnel peer state and VNC availability

Main components:

- `client/internal/agent`
- `client/internal/api_client`
- `client/internal/nat`
- `client/internal/peer`
- `client/internal/tunnel`
- `client/internal/vnc`

### Web Dashboard

Responsibilities:

- Authentication UX
- Network and peer visibility
- API key and configuration visibility

Main components:

- `web/src/pages`
- `web/src/components`
- `web/src/store`
- `web/src/api`

## Security Layers

- HTTPS + TLS 1.3 for API traffic
- JWT for user sessions
- API keys for machine endpoints
- WireGuard-style cryptographic key material in client tunnel layer
- Zero-trust peer verification via explicit coordination metadata
