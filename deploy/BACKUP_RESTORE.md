# Database backup and restore

The application database is backed up independently from generated/uploaded media. Use a secret-manager supplied `DATABASE_URL` or `BACKUP_DATABASE_URL`; do not place credentials in a file committed to the repository.

Create an owner-only PostgreSQL custom-format archive:

```bash
BACKUP_DATABASE_URL="$DATABASE_URL" BACKUP_DIR=/secure/backups make database-backup
```

The command writes an archive atomically, verifies it with `pg_restore --list`, and writes a SHA-256 sidecar. It does not print the database URL or application data.

Restore only into an explicitly selected target. The confirmation string is deliberately long to make an accidental restore difficult:

```bash
RESTORE_DATABASE_URL="$RESTORE_TARGET_URL" \
RESTORE_CONFIRM=I_UNDERSTAND_DATA_WILL_BE_REPLACED \
  make database-restore ARCHIVE=/secure/backups/hcai-....dump
```

Restore uses `--clean --if-exists --exit-on-error`, checks the archive checksum when a sidecar exists, and verifies `schema_migrations` after completion. It deliberately does not claim single-transaction atomicity because the full schema can exceed PostgreSQL's `max_locks_per_transaction`; restore into an isolated database, verify it, and only then switch traffic. It never falls back to `DATABASE_URL`.

Production acceptance still requires an external backup schedule, encrypted off-host retention, restore into an isolated environment, media/object-store backup and deletion propagation, RPO/RTO evidence, access auditing, and a tested rollback plan.
