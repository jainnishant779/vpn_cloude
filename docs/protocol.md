# Protocol Specification

## Relay Packet Format

Each packet uses:

- `1 byte`: packet type
- `16 bytes`: session ID
- `N bytes`: payload

Packet types:

- `0x01`: CONNECT
- `0x02`: DATA
- `0x03`: DISCONNECT
- `0x04`: PING

## CONNECT Payload

JSON payload:

```json
{
  "session_token": "shared-token",
  "peer_id": "peer-uuid"
}
```

Behavior:

1. First peer creates session and waits.
2. Second peer with same token is paired.
3. Relay emits control packet indicating paired state.

## Data Forwarding

- Relay forwards DATA packet to the opposite session peer.
- Session `last_activity` is refreshed on packet transit.
- Idle sessions are removed after 5 minutes.

## Coordination Announce Payload

```json
{
  "peer_id": "peer-uuid",
  "network_id": "network-uuid",
  "public_endpoint": "ip:port",
  "local_endpoints": ["lan-ip:port"]
}
```

Redis storage:

- Entry key: `coord:announce:{network}:{peer}` (TTL 60s)
- Index key: `coord:network:{network}:peers` (TTL 70s)

## Handshake Payload (Peer Utility)

```json
{
  "magic": "QTHS1",
  "peer_id": "peer-uuid",
  "timestamp": 1700000000,
  "hmac": "hex-signature"
}
```

Signature input:

- `magic|peer_id|timestamp`
- HMAC-SHA256 with shared secret
