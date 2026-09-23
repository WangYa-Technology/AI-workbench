#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "$0")/.." && pwd)"
base_database_url="${TEST_DATABASE_URL:-postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable}"
e2e_api_port="${E2E_API_PORT:-18080}"
e2e_web_port="${E2E_WEB_PORT:-15173}"
e2e_schema="hcai_e2e"
e2e_root="$(mktemp -d "${TMPDIR:-/tmp}/hcai-e2e.XXXXXX")"
e2e_media_root="$project_root/data/e2e-media"
api_pid=""
worker_pid=""
web_pid=""
fixture_pid=""
e2e_payment_fixture="${E2E_PAYMENT_FIXTURE:-0}"
e2e_fixture_port="${E2E_FIXTURE_PORT:-18084}"

cleanup() {
  for pid in "$web_pid" "$worker_pid" "$api_pid" "$fixture_pid"; do
    if [[ -n "$pid" ]]; then
      kill -TERM "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  # Keep isolated-service diagnostics alongside Playwright failure artifacts.
  mkdir -p "$project_root/web/test-results/service-logs"
  for log in api worker fixture; do
    if [[ -f "$e2e_root/$log.log" ]]; then
      cp "$e2e_root/$log.log" "$project_root/web/test-results/service-logs/$log.log"
    fi
  done
  ./scripts/e2e-cleanup.sh >/dev/null 2>&1 || true
  rm -rf "$e2e_root"
}
trap cleanup EXIT INT TERM

if [[ "$e2e_payment_fixture" != 0 && "$e2e_payment_fixture" != 1 ]]; then
  echo "E2E_PAYMENT_FIXTURE must be 0 or 1." >&2
  exit 1
fi
ports=("$e2e_api_port" "$e2e_web_port")
if [[ "$e2e_payment_fixture" == 1 ]]; then
  ports+=("$e2e_fixture_port")
  if [[ "$e2e_fixture_port" == "$e2e_api_port" || "$e2e_fixture_port" == "$e2e_web_port" ]]; then
    echo "E2E fixture port must differ from app ports." >&2
    exit 1
  fi
fi
for port in "${ports[@]}"; do
  if ! [[ "$port" =~ ^[0-9]+$ ]] || (( port < 1024 || port > 65535 )); then
    echo "E2E ports must be between 1024 and 65535." >&2
    exit 1
  fi
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "E2E port $port is already in use." >&2
    exit 1
  fi
done

cd "$project_root"
rm -rf "$e2e_media_root"
mkdir -p "$e2e_media_root"
if [[ -z "${TEST_DATABASE_URL:-}" ]]; then
  docker compose up -d postgres >/dev/null
fi
./scripts/e2e-cleanup.sh
PGOPTIONS="-c search_path=public" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc \
  "CREATE SCHEMA $e2e_schema" >/dev/null

database_url_separator="?"
if [[ "$base_database_url" == *"?"* ]]; then
  database_url_separator="&"
fi
export APP_ENV=test
export DATABASE_URL="${base_database_url}${database_url_separator}search_path=${e2e_schema}"
export HTTP_ADDR="127.0.0.1:${e2e_api_port}"
export WEB_ORIGIN="http://127.0.0.1:${e2e_web_port}"
export MEDIA_ROOT="$e2e_media_root"
export LOCAL_PROVIDER_SOURCE="$project_root/web/public/media/home-cinematic.jpg"
export LOCAL_PROVIDER_ENABLED=true
export EMAIL_DELIVERY_MODE=local_file
export VITE_API_TARGET="http://127.0.0.1:${e2e_api_port}"

# The payment browser suite can only use this loopback, synthetic test merchant.
# Never inherit a real payment configuration from the caller's environment.
export PAYMENT_PROVIDER=stripe WAFFO_ENABLED=false
export STRIPE_ENABLED=false STRIPE_LIVE_MODE=false STRIPE_LIVE_MODE_APPROVED=false
if [[ "$e2e_payment_fixture" == 1 ]]; then
  export STRIPE_ENABLED=true STRIPE_SECRET_KEY=sk_test_payment_drill
  export STRIPE_WEBHOOK_SECRET=whsec_payment_drill_secret STRIPE_API_VERSION=2026-02-25.clover
  export STRIPE_BASE_URL="http://127.0.0.1:${e2e_fixture_port}/v1"
  export STRIPE_WEBHOOK_TOLERANCE_SECONDS=300
  go build -o "$e2e_root/stripe-fixture" ./cmd/stripefixture
  "$e2e_root/stripe-fixture" -addr "127.0.0.1:${e2e_fixture_port}" >"$e2e_root/fixture.log" 2>&1 & fixture_pid=$!
  for _ in {1..100}; do
    curl -fsS "http://127.0.0.1:${e2e_fixture_port}/ready" >/dev/null 2>&1 && break
    sleep 0.1
  done
  curl -fsS "http://127.0.0.1:${e2e_fixture_port}/ready" >/dev/null
fi

go run ./cmd/migrate >/dev/null
go run ./internal/testfixtures/cmd/seed >/dev/null

go run ./cmd/api >"$e2e_root/api.log" 2>&1 & api_pid=$!
go run ./cmd/worker >"$e2e_root/worker.log" 2>&1 & worker_pid=$!
npm --prefix web run dev -- --host 127.0.0.1 --port "$e2e_web_port" --strictPort >"$e2e_root/web.log" 2>&1 & web_pid=$!

wait "$api_pid" "$worker_pid" "$web_pid"
