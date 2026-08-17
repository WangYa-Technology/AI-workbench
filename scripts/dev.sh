#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
docker compose up -d postgres
go run ./cmd/migrate
if [[ "${HCAI_SEED_DEMO:-0}" == "1" ]]; then
  export DEMO_DATA_ENABLED=true
  go run ./cmd/seed
fi

cleanup() {
  kill "${api_pid:-}" "${worker_pid:-}" "${web_pid:-}" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

go run ./cmd/api & api_pid=$!
go run ./cmd/worker & worker_pid=$!
npm --prefix web run dev -- --host 127.0.0.1 & web_pid=$!

wait "$api_pid" "$worker_pid" "$web_pid"
