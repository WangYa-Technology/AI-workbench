.PHONY: bootstrap dev dev-demo api worker web db-up db-down migrate seed test e2e lint build recovery-drill

bootstrap:
	go mod download
	npm --prefix web install

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

migrate:
	go run ./cmd/migrate

seed:
	go run ./cmd/seed

api:
	go run ./cmd/api

worker:
	go run ./cmd/worker

web:
	npm --prefix web run dev

dev:
	./scripts/dev.sh

dev-demo:
	HCAI_SEED_DEMO=1 ./scripts/dev.sh

test:
	go test ./...
	npm --prefix web run test

e2e:
	npm --prefix web run test:e2e

lint:
	go vet ./...
	npm --prefix web run lint
	npm --prefix web run typecheck

build:
	go build ./cmd/api ./cmd/worker ./cmd/migrate ./cmd/seed
	npm --prefix web run build

recovery-drill:
	./scripts/worker-restart-drill.sh
