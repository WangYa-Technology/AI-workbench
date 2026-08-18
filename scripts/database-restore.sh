#!/usr/bin/env bash
set -euo pipefail

archive="${1:-}"
target_url="${RESTORE_DATABASE_URL:-}"
if [[ -z "$archive" || ! -f "$archive" ]]; then
  echo "Usage: RESTORE_DATABASE_URL=... RESTORE_CONFIRM=I_UNDERSTAND_DATA_WILL_BE_REPLACED $0 backup.dump" >&2
  exit 1
fi
if [[ -z "$target_url" ]]; then
  echo "RESTORE_DATABASE_URL is required; refusing to use DATABASE_URL implicitly." >&2
  exit 1
fi
if [[ "${RESTORE_CONFIRM:-}" != "I_UNDERSTAND_DATA_WILL_BE_REPLACED" ]]; then
  echo "RESTORE_CONFIRM must equal I_UNDERSTAND_DATA_WILL_BE_REPLACED." >&2
  exit 1
fi
for command in pg_restore psql; do
  command -v "$command" >/dev/null || { echo "$command is required." >&2; exit 1; }
done

if [[ -f "$archive.sha256" ]]; then
  if command -v shasum >/dev/null; then
    shasum -a 256 -c "$archive.sha256" >/dev/null
  else
    sha256sum -c "$archive.sha256" >/dev/null
  fi
fi
pg_restore --list "$archive" >/dev/null

# --clean is intentionally gated above. A single transaction is not used here:
# large HCAI schemas can exceed PostgreSQL's max_locks_per_transaction while
# restoring thousands of constraints. The explicit target, exit-on-error, and
# post-restore invariants make partial restores visible and keep this command
# suitable for an isolated restore rehearsal before traffic is switched.
pg_restore --clean --if-exists --exit-on-error --no-owner --no-acl --dbname="$target_url" "$archive"
restored_migrations="$(psql "$target_url" -X -v ON_ERROR_STOP=1 -Atqc "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='schema_migrations'")"
if [[ "$restored_migrations" != "1" ]]; then
  echo "Restore completed without the schema_migrations table." >&2
  exit 1
fi
latest_migration="$(psql "$target_url" -X -v ON_ERROR_STOP=1 -Atqc "SELECT coalesce(max(version),'none') FROM public.schema_migrations")"
printf 'restored=%s\nlatest_migration=%s\n' "$archive" "$latest_migration"
