#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "$0")/.." && pwd)"
base_database_url="${TEST_DATABASE_URL:-postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable}"
drill_http_port="${DRILL_HTTP_PORT:-18081}"
drill_schema="drill_worker_restart_$(date +%s)_${RANDOM}"
drill_root="$(mktemp -d "${TMPDIR:-/tmp}/hcai-worker-restart.XXXXXX")"
api_pid=""
worker_pid=""

cleanup() {
  if [[ -n "$worker_pid" ]]; then
    kill -TERM "$worker_pid" 2>/dev/null || true
    wait "$worker_pid" 2>/dev/null || true
  fi
  if [[ -n "$api_pid" ]]; then
    kill -TERM "$api_pid" 2>/dev/null || true
    wait "$api_pid" 2>/dev/null || true
  fi
  PGOPTIONS="-c search_path=public" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc "DROP SCHEMA IF EXISTS $drill_schema CASCADE" >/dev/null 2>&1 || true
  rm -rf "$drill_root"
}
trap cleanup EXIT INT TERM

if ! [[ "$drill_http_port" =~ ^[0-9]+$ ]] || (( drill_http_port < 1024 || drill_http_port > 65535 )); then
  echo "DRILL_HTTP_PORT must be an unused port from 1024 to 65535." >&2
  exit 1
fi
if lsof -nP -iTCP:"$drill_http_port" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "DRILL_HTTP_PORT $drill_http_port is already in use." >&2
  exit 1
fi

cd "$project_root"
PGOPTIONS="-c search_path=public" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc "CREATE SCHEMA $drill_schema" >/dev/null

database_url_separator="?"
if [[ "$base_database_url" == *"?"* ]]; then
  database_url_separator="&"
fi
database_url="${base_database_url}${database_url_separator}search_path=${drill_schema}"
export APP_ENV=test
export DATABASE_URL="$database_url"
export HTTP_ADDR="127.0.0.1:${drill_http_port}"
export WEB_ORIGIN="http://127.0.0.1:${drill_http_port}"
export MEDIA_ROOT="$drill_root/media"
export LOCAL_PROVIDER_SOURCE="$project_root/web/public/media/home-cinematic.jpg"
export LOCAL_PROVIDER_ENABLED=true
export EMAIL_DELIVERY_MODE=disabled

go run ./cmd/migrate >/dev/null
go run ./cmd/seed >/dev/null
go build -o "$drill_root/hcai-api" ./cmd/api
go build -o "$drill_root/hcai-worker" ./cmd/worker

"$drill_root/hcai-api" >"$drill_root/api.log" 2>&1 &
api_pid=$!
for _ in {1..100}; do
  if curl -fsS "http://127.0.0.1:${drill_http_port}/ready" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
curl -fsS "http://127.0.0.1:${drill_http_port}/ready" >/dev/null

"$drill_root/hcai-worker" >"$drill_root/worker-before.log" 2>&1 &
worker_pid=$!
for _ in {1..100}; do
  if rg -q '"msg":"worker started"' "$drill_root/worker-before.log"; then
    break
  fi
  sleep 0.1
done
rg -q '"msg":"worker started"' "$drill_root/worker-before.log"
kill -TERM "$worker_pid"
wait "$worker_pid"
worker_pid=""

cookie_jar="$drill_root/creator.cookies"
curl -fsS -c "$cookie_jar" -b "$cookie_jar" \
  -H 'Content-Type: application/json' \
  -d '{"actor":"creator"}' \
  "http://127.0.0.1:${drill_http_port}/api/v1/auth/demo" >/dev/null

idempotency_key="worker-restart-drill-$(date +%s)-${RANDOM}"
submission=$(curl -fsS -c "$cookie_jar" -b "$cookie_jar" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $idempotency_key" \
  -d '{"mode":"image","prompt":"CP-40 durable worker restart recovery evidence"}' \
  "http://127.0.0.1:${drill_http_port}/api/v1/generations")
generation_id=$(jq -er '.id' <<<"$submission")

sql() {
  PGOPTIONS="-c search_path=$drill_schema" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc "$1"
}

before=$(sql "SELECT g.status||'|'||r.status||'|'||(SELECT count(*) FROM assets WHERE source_type='generation' AND source_id=g.id)||'|'||(SELECT count(*) FROM billing_entries WHERE operation_id=g.id AND entry_type='generation_charge') FROM generations g JOIN billing_reservations r ON r.operation_id=g.id AND r.operation_type='generation' WHERE g.id='$generation_id'")
if [[ "$before" != "queued|held|0|0" ]]; then
  echo "Unexpected offline evidence: $before" >&2
  exit 1
fi

"$drill_root/hcai-worker" >"$drill_root/worker-after.log" 2>&1 &
worker_pid=$!
generation=""
for _ in {1..200}; do
  generation=$(curl -fsS -c "$cookie_jar" -b "$cookie_jar" "http://127.0.0.1:${drill_http_port}/api/v1/generations/${generation_id}")
  if [[ "$(jq -r '.status' <<<"$generation")" == "succeeded" ]]; then
    break
  fi
  sleep 0.1
done
if [[ "$(jq -r '.status' <<<"$generation")" != "succeeded" ]]; then
  echo "Generation did not recover after worker restart." >&2
  exit 1
fi

replay=$(curl -fsS -c "$cookie_jar" -b "$cookie_jar" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $idempotency_key" \
  -d '{"mode":"image","prompt":"CP-40 durable worker restart recovery evidence"}' \
  "http://127.0.0.1:${drill_http_port}/api/v1/generations")
if [[ "$(jq -r '.id' <<<"$replay")" != "$generation_id" ]]; then
  echo "Idempotent replay returned a different generation." >&2
  exit 1
fi

after=$(sql "SELECT g.status||'|'||r.status||'|'||(SELECT count(*) FROM assets WHERE source_type='generation' AND source_id=g.id)||'|'||(SELECT count(*) FROM billing_entries WHERE operation_id=g.id AND entry_type='generation_charge')||'|'||j.status||'|'||j.attempts||'|'||(SELECT count(*) FROM job_attempts WHERE job_id=j.id AND status='succeeded') FROM generations g JOIN billing_reservations r ON r.operation_id=g.id AND r.operation_type='generation' JOIN jobs j ON j.kind='generation.generate' AND j.payload->>'generationId'=g.id::text WHERE g.id='$generation_id'")
if [[ "$after" != "succeeded|captured|1|1|succeeded|1|1" ]]; then
  echo "Unexpected recovery evidence: $after" >&2
  exit 1
fi

admin_jar="$drill_root/admin.cookies"
curl -fsS -c "$admin_jar" -b "$admin_jar" \
  -H 'Content-Type: application/json' \
  -d '{"actor":"admin"}' \
  "http://127.0.0.1:${drill_http_port}/api/v1/auth/demo" >/dev/null
diagnostics=$(curl -fsS -c "$admin_jar" -b "$admin_jar" "http://127.0.0.1:${drill_http_port}/api/v1/admin/observability")
jq -e '.databaseReady == true and .audit.valid == true and .jobs.attemptsLast24Hours >= 1 and .jobs.byAttemptStatus.succeeded >= 1' <<<"$diagnostics" >/dev/null

jq -n \
  --arg generationId "$generation_id" \
  --arg before "$before" \
  --arg after "$after" \
  --arg outputAssetId "$(jq -r '.outputAssetId' <<<"$generation")" \
  '{status:"passed", generationId:$generationId, beforeRestart:$before, afterRestart:$after, outputAssetId:$outputAssetId, idempotentReplay:true, diagnosticsVisible:true}'
