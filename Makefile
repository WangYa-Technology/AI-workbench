.PHONY: bootstrap dev api worker web waffo-connector db-up db-down migrate test-fixtures test test-integration e2e e2e-payments lint security build production-config-check media-staging-check media-application-staging-check provider-staging-check creative-provider-staging-check container-build database-backup database-restore metrics-check recovery-drill payment-drill billing-drill provider-drill

# Payment integration fixtures each apply the full migration history. The
# complete package now exceeds 30 minutes; keep a finite, overridable budget.
GO_TEST_TIMEOUT ?= 60m

.PHONY: proxy-test runtime-image-check

RUNTIME_IMAGES ?= hcai-chat-api:local hcai-chat-worker:local hcai-chat-migrate:local

bootstrap:
	go mod download
	npm --prefix web install
	npm --prefix services/waffo-connector ci

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

migrate:
	go run ./cmd/migrate

test-fixtures:
	go run ./internal/testfixtures/cmd/seed

api:
	go run ./cmd/api

worker:
	go run ./cmd/worker

web:
	npm --prefix web run dev

waffo-connector:
	npm --prefix services/waffo-connector start

dev:
	./scripts/dev.sh

test:
	HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -count=1 -timeout $(GO_TEST_TIMEOUT) ./...
	npm --prefix services/waffo-connector test
	npm --prefix web run test

test-integration:
	@test -n "$(TEST_DATABASE_URL)" || (echo "TEST_DATABASE_URL is required for integration tests." >&2; exit 1)
	HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -count=1 -timeout $(GO_TEST_TIMEOUT) ./...

e2e:
	npm --prefix web run test:e2e

e2e-payments:
	npm --prefix web run test:e2e:payments

# Requires locally prepared web and Node images; this never starts the app/DB.
proxy-test:
	python3 scripts/test-production-proxy.py

lint:
	go vet ./...
	npm --prefix web run lint
	npm --prefix web run typecheck

# Text output preserves govulncheck's nonzero exit status for imported-package findings.
# The official npm registry supports auditing; some package mirrors do not.
security:
	go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 -scan=package ./...
	npm --prefix web audit --package-lock-only --omit=dev --audit-level=low --registry=https://registry.npmjs.org
	npm --prefix services/waffo-connector audit --package-lock-only --omit=dev --audit-level=low --registry=https://registry.npmjs.org

build:
	go build ./cmd/api ./cmd/worker ./cmd/migrate ./cmd/configcheck ./cmd/mediacheck ./cmd/mediaappcheck ./cmd/providercheck ./cmd/creativeprovidercheck
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
	docker build -f deploy/Dockerfile.runtime --build-arg APP=migrate -t hcai-chat-migrate:local .
	$(MAKE) runtime-image-check
	docker build -f deploy/Dockerfile.web -t hcai-chat-web:local .

runtime-image-check:
	python3 scripts/check-runtime-images.py $(RUNTIME_IMAGES)

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

billing-drill:
	./scripts/billing-provider-drill.sh

provider-drill:
	./scripts/provider-runtime-drill.sh
