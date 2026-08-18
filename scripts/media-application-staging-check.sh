#!/usr/bin/env bash
set -euo pipefail

confirmation="I_APPROVE_MEDIA_APPLICATION_ACCEPTANCE_CALLS"
if [[ "${MEDIA_APPLICATION_ACCEPTANCE_CONFIRM:-}" != "$confirmation" ]]; then
  echo "media application acceptance disabled: exact confirmation is required" >&2
  exit 1
fi

project_root="$(cd "$(dirname "$0")/.." && pwd)"
base_database_url="${MEDIA_APPLICATION_DATABASE_URL:-}"
if [[ -z "$base_database_url" ]]; then
  echo "MEDIA_APPLICATION_DATABASE_URL must identify an approved disposable staging database" >&2
  exit 1
fi
if [[ "$base_database_url" == *"search_path="* ]]; then
  echo "MEDIA_APPLICATION_DATABASE_URL must not contain search_path; the check creates an isolated schema" >&2
  exit 1
fi
for command in psql go; do
  command -v "$command" >/dev/null || { echo "media application acceptance requires $command" >&2; exit 1; }
done

schema="hcai_media_acceptance_$(date +%Y%m%d%H%M%S)_${RANDOM}"
created=false
cleanup() {
  if [[ "$created" == true ]]; then
    PGOPTIONS="-c search_path=public" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc "DROP SCHEMA IF EXISTS $schema CASCADE" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

PGOPTIONS="-c search_path=public" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc "CREATE SCHEMA $schema" >/dev/null
created=true
separator="?"
if [[ "$base_database_url" == *"?"* ]]; then separator="&"; fi

export APP_ENV=staging
export DATABASE_URL="${base_database_url}${separator}search_path=${schema}"
export MEDIA_APPLICATION_ACCEPTANCE_SCHEMA="$schema"
export LOCAL_PROVIDER_ENABLED=false
export EMAIL_DELIVERY_MODE=disabled

cd "$project_root"
go run ./cmd/mediaappcheck
