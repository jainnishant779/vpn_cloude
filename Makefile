.PHONY: build-server build-relay build-client test docker-up docker-down migrate

GO ?= go

build-server:
	cd server && $(GO) build ./...

build-relay:
	cd relay && $(GO) build ./...

build-client:
	cd client && $(GO) build ./...

test:
	cd pkg && $(GO) test ./...
	cd server && $(GO) test ./...
	cd relay && $(GO) test ./...
	cd client && $(GO) test ./...

docker-up:
	docker compose up -d postgres redis

docker-down:
	docker compose down

migrate:
	cd server && $(GO) run ./cmd/server --migrate-only
