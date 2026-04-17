# Troubleshooting

## Go or Docker Command Not Found

Symptoms:

- `go` or `docker` fails with command not found.

Fix:

- Install Go 1.21+ and Docker Desktop.
- Restart terminal session.

## Server Does Not Start

Checks:

1. Verify `DB_URL` and `REDIS_URL` values.
2. Confirm Postgres and Redis are healthy:
   - `docker compose ps`
3. Verify migrations folder exists under `server/internal/database/migrations`.

## 401 on API Calls

Checks:

- JWT routes require `Authorization: Bearer <token>`.
- Agent routes require `X-API-Key`.
- Refresh expired JWT via `POST /api/v1/auth/refresh`.

## Peer Not Visible in Coordination List

Checks:

1. Ensure heartbeat endpoint is called regularly.
2. Ensure announce endpoint is called every endpoint update.
3. Verify Redis is reachable from server.

## Relay Assignment Fails

Checks:

- Confirm relay rows exist in `relay_servers` table.
- Confirm relay health status is true and `current_load < max_load`.

## Web Dashboard Type Errors

Symptoms:

- Module resolution errors in `web/` TypeScript files.

Fix:

- Install dependencies in `web/`:
  - `npm install`
- Ensure Node.js and npm are available on PATH.
