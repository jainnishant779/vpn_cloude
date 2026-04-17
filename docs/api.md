# API Reference

Base path: `/api/v1`

## Auth

- `POST /auth/register`
- `POST /auth/login`
- `POST /auth/refresh`

Response envelope:

```json
{
  "success": true,
  "data": {},
  "error": ""
}
```

## Networks (JWT-protected)

- `GET /networks`
- `POST /networks`
- `GET /networks/{id}`
- `PUT /networks/{id}`
- `DELETE /networks/{id}`
- `GET /networks/{id}/peers`

## Peer + Coordination (API key protected)

- `POST /networks/{id}/peers/register`
- `PUT /networks/{id}/peers/{peerId}/heartbeat`
- `POST /coord/announce`
- `GET /coord/peers/{networkId}`
- `GET /coord/relay/assign?peer_id=<id>`

## Health

- `GET /health`

## Auth Headers

JWT endpoints:

- `Authorization: Bearer <access-token>`

Machine endpoints:

- `X-API-Key: <api-key>`
