# Setup Guide

## Prerequisites

- Go 1.21+
- Docker + Docker Compose
- Node.js 20+ and npm (for dashboard)

## Environment

1. Copy `.env.example` values to your runtime shell.
2. Ensure values for:
   - `DB_URL`
   - `REDIS_URL`
   - `JWT_SECRET`
   - `SERVER_PORT`
   - `RELAY_PORT`

## Start Infrastructure

- `docker compose up -d postgres redis`

## Run Control Plane

- `cd server`
- `go run ./cmd/server`

## Run Relay

- `cd relay`
- `go run ./cmd/relay`

## Run Client Agent

1. Authenticate:
   - `quicktunnel login --email <email> --password <password>`
2. Set config:
   - `quicktunnel config --set server_url=http://localhost:8080`
   - `quicktunnel config --set network_id=<network-id>`
3. Start:
   - `quicktunnel up`

## Run Web Dashboard

- `cd web`
- `npm install`
- `npm run dev`

## Full Stack via Compose

- `docker compose up --build`

Services:

- API server: `http://localhost:8080`
- Relay health: `http://localhost:8081/health`
- Web UI: `http://localhost:3000`
