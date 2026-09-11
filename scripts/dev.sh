#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

# Keep the Pancake private key inside the connector process. A developer may
# export it for this convenience script, but API/Worker must not inherit it.
waffo_connector_private_key="${WAFFO_PRIVATE_KEY-}"
waffo_connector_private_key_base64="${WAFFO_PRIVATE_KEY_BASE64-}"
unset WAFFO_PRIVATE_KEY WAFFO_PRIVATE_KEY_BASE64

docker compose up -d postgres
go run ./cmd/migrate
if [[ "${HCAI_SEED_DEMO:-0}" == "1" ]]; then
  export DEMO_DATA_ENABLED=true
  go run ./cmd/seed
fi

cleanup() {
  kill "${api_pid:-}" "${worker_pid:-}" "${web_pid:-}" "${waffo_connector_pid:-}" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

go run ./cmd/api & api_pid=$!
go run ./cmd/worker & worker_pid=$!
npm --prefix web run dev -- --host 127.0.0.1 & web_pid=$!

# The connector owns the Waffo private key. Start it only when the deployment
# explicitly enables Waffo and has injected the connector environment.
if [[ "${WAFFO_ENABLED:-false}" == "true" ]]; then
  WAFFO_PRIVATE_KEY="$waffo_connector_private_key" \
  WAFFO_PRIVATE_KEY_BASE64="$waffo_connector_private_key_base64" \
  npm --prefix services/waffo-connector start & waffo_connector_pid=$!
fi

if [[ -n "${waffo_connector_pid:-}" ]]; then
  wait "$api_pid" "$worker_pid" "$web_pid" "$waffo_connector_pid"
else
  wait "$api_pid" "$worker_pid" "$web_pid"
fi
