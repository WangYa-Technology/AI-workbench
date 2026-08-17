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

cleanup() {
  for pid in "$web_pid" "$worker_pid" "$api_pid"; do
    if [[ -n "$pid" ]]; then
      kill -TERM "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  ./scripts/e2e-cleanup.sh >/dev/null 2>&1 || true
  rm -rf "$e2e_root"
}
trap cleanup EXIT INT TERM

for port in "$e2e_api_port" "$e2e_web_port"; do
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
export DEMO_DATA_ENABLED=true
export EMAIL_DELIVERY_MODE=local_file
export VITE_API_TARGET="http://127.0.0.1:${e2e_api_port}"

go run ./cmd/migrate >/dev/null
go run ./cmd/seed >/dev/null

go run ./cmd/api >"$e2e_root/api.log" 2>&1 & api_pid=$!
go run ./cmd/worker >"$e2e_root/worker.log" 2>&1 & worker_pid=$!
npm --prefix web run dev -- --host 127.0.0.1 --port "$e2e_web_port" --strictPort >"$e2e_root/web.log" 2>&1 & web_pid=$!

wait "$api_pid" "$worker_pid" "$web_pid"
