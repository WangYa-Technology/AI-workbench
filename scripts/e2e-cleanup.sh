#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "$0")/.." && pwd)"
base_database_url="${TEST_DATABASE_URL:-postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable}"

PGOPTIONS="-c search_path=public -c client_min_messages=warning" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc \
  "DROP SCHEMA IF EXISTS hcai_e2e CASCADE" >/dev/null
rm -rf "$project_root/data/e2e-media"
