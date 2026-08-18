.PHONY: bootstrap dev dev-demo api worker web db-up db-down migrate seed test e2e lint build production-config-check media-staging-check media-application-staging-check provider-staging-check creative-provider-staging-check container-build database-backup database-restore metrics-check recovery-drill payment-drill provider-drill

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
	go build ./cmd/api ./cmd/worker ./cmd/migrate ./cmd/seed ./cmd/configcheck ./cmd/mediacheck ./cmd/mediaappcheck ./cmd/providercheck ./cmd/creativeprovidercheck
	npm --prefix web run build

production-config-check:
	go run ./cmd/configcheck -require-production

media-staging-check:
	go run ./cmd/mediacheck

media-application-staging-check:
	./scripts/media-application-staging-check.sh

provider-staging-check:
	go run ./cmd/providercheck

creative-provider-staging-check:
	go run ./cmd/creativeprovidercheck

container-build:
	docker build -f deploy/Dockerfile.runtime --build-arg APP=api -t hcai-chat-api:local .
	docker build -f deploy/Dockerfile.runtime --build-arg APP=worker -t hcai-chat-worker:local .
	docker build -f deploy/Dockerfile.web -t hcai-chat-web:local .

database-backup:
	./scripts/database-backup.sh

database-restore:
	test -n "$(ARCHIVE)" || (echo "ARCHIVE=/path/to/backup.dump is required." >&2; exit 1)
	./scripts/database-restore.sh "$(ARCHIVE)"

metrics-check:
	./scripts/metrics-alert-check.sh

recovery-drill:
	./scripts/worker-restart-drill.sh

payment-drill:
	./scripts/payment-provider-drill.sh

provider-drill:
	./scripts/provider-runtime-drill.sh
