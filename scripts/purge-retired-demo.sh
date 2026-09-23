#!/usr/bin/env bash
set -euo pipefail

# Explicit maintenance command; never called by startup or migrations.
case "${APP_ENV:-}" in
  development|test) ;;
  *) echo 'This cleanup is limited to development/test databases.' >&2; exit 1 ;;
esac
if [[ "${1:-}" != "--apply" ]]; then
  echo 'Usage: APP_ENV=development DATABASE_URL=... scripts/purge-retired-demo.sh --apply' >&2
  exit 1
fi
project_root="$(cd "$(dirname "$0")/.." && pwd)"
export PGOPTIONS="${PGOPTIONS:-} -c hcai.retired_fixture_purge=${APP_ENV}"
psql "${DATABASE_URL:?DATABASE_URL is required}" -X --single-transaction \
  -v ON_ERROR_STOP=1 -f "$project_root/scripts/sql/purge-retired-demo.sql"
