#!/usr/bin/env bash
set -euo pipefail

# The database URL is read from the environment so credentials never enter a
# repository file. Use PGPASSFILE or the deployment secret manager for auth.
database_url="${BACKUP_DATABASE_URL:-${DATABASE_URL:-}}"
backup_dir="${BACKUP_DIR:-./backups}"
if [[ -z "$database_url" ]]; then
  echo "BACKUP_DATABASE_URL or DATABASE_URL is required." >&2
  exit 1
fi
if [[ "$backup_dir" == "/" || "$backup_dir" == "." ]]; then
  echo "BACKUP_DIR must be a dedicated backup directory, not the current or root directory." >&2
  exit 1
fi
for command in pg_dump pg_restore; do
  command -v "$command" >/dev/null || { echo "$command is required." >&2; exit 1; }
done

umask 077
mkdir -p "$backup_dir"
chmod 700 "$backup_dir"
timestamp="$(date -u '+%Y%m%dT%H%M%SZ')"
temporary="$(mktemp "$backup_dir/.hcai-backup.XXXXXX.dump")"
archive=""
cleanup() {
  if [[ -n "$temporary" && -e "$temporary" ]]; then rm -f "$temporary"; fi
}
trap cleanup EXIT INT TERM

pg_dump --format=custom --no-owner --no-acl --file="$temporary" "$database_url"
pg_restore --list "$temporary" >/dev/null
archive="$backup_dir/hcai-${timestamp}-$$.dump"
mv "$temporary" "$archive"
temporary=""

if command -v shasum >/dev/null; then
  shasum -a 256 "$archive" >"$archive.sha256"
else
  sha256sum "$archive" >"$archive.sha256"
fi
chmod 600 "$archive" "$archive.sha256"
printf 'backup=%s\nchecksum=%s\n' "$archive" "$archive.sha256"
