# Database backup and restore

The application database is backed up independently from generated/uploaded media. Use a secret-manager supplied `DATABASE_URL` or `BACKUP_DATABASE_URL`; do not place credentials in a file committed to the repository.

Create an owner-only PostgreSQL custom-format archive:

```bash
BACKUP_DATABASE_URL="$DATABASE_URL" BACKUP_DIR=/secure/backups make database-backup
```

The command writes an archive atomically, verifies it with `pg_restore --list`, and writes a SHA-256 sidecar. It does not print the database URL or application data.

Restore only into an explicitly selected, newly created, offline target with no API/worker connected. Keep Providers disabled during validation. The script does not enforce that the target is empty or isolated. The confirmation string is deliberately long to make an accidental restore difficult:

```bash
RESTORE_DATABASE_URL="$RESTORE_TARGET_URL" \
RESTORE_CONFIRM=I_UNDERSTAND_DATA_WILL_BE_REPLACED \
  make database-restore ARCHIVE=/secure/backups/hcai-....dump
```

Restore uses `--clean --if-exists --exit-on-error`, checks the archive checksum when a sidecar exists, and verifies `schema_migrations` after completion. It deliberately does not claim single-transaction atomicity because the full schema can exceed PostgreSQL's `max_locks_per_transaction`; restore into an isolated database, verify it, and only then switch traffic. It never falls back to `DATABASE_URL`.

Production acceptance still requires an external backup schedule, encrypted off-host retention, restore into an isolated environment, media/object-store backup and deletion propagation, RPO/RTO evidence, access auditing, and a tested rollback plan.

## Verified limits and rehearsal (2026-09-27)

Target 7 used only the dedicated loopback PostgreSQL container on port 55435. Empty and baseline upgrades, worker restart, and two new-database restores are recorded in [the migration/recovery review](../docs/resource-marketplace-migration-recovery-review.md). This does not establish production RPO/RTO or media recovery. Keep the archive, source manifest, logs and restored databases until evidence review finishes.

Operational details that the scripts do not enforce:

- `pg_restore --list` validates the archive catalog, not all table contents. The post-check verifies only `public.schema_migrations` existence and its maximum filename; compare the full migration list, row counts/content digests, constraints, valid indexes, enabled triggers, jobs/attempts and financial evidence before allowing traffic.
- The archive rename is atomic; publication of archive and checksum together is not. Restore accepts a missing checksum sidecar. Require a trusted checksum externally and verify it against the actual selected archive; the generated sidecar contains its original pathname, so moving files requires explicit path handling. SHA-256 is not an authenticity signature.
- Restore is not one transaction. On any failure, keep the partial target offline for inspection and rehearse into another fresh target. Do not resume application writes just because some tables are present. `--clean` drops archive-listed objects, not necessarily every extra target object.
- Dumps omit owner/ACL restoration and do not include cluster roles, application secrets or media bytes. Restore role grants, encryption keys and the matching media/object versions separately under controlled procedures. Database and object-store retention must cover unresolved refunds, disputes and source-return evidence.
- A restored old database cannot undo an external payout. Reconcile post-backup Provider events using the original merchant, object IDs, immutable command and idempotency keys before restarting senders. Never clear journals or create replacement send keys to make a replay succeed.
- The backup script can fall back to `DATABASE_URL`; always supply an explicit rehearsal source. Restore requires its separate URL and confirmation but has no host allowlist. PostgreSQL utilities receive the URL as a process argument: use controlled credentials, avoid command tracing and do not publish process listings containing credentials.

The original `scripts/worker-restart-drill.sh` is not a current acceptance gate: its billing-reservation/entry assertions predate point billing and `.audit.valid` no longer exists in diagnostics. It also defaults to port 5432 and deletes its schema/files on exit. Target 7 retained a temporary copy, pinned port 55435, disabled that cleanup, and used `point_reservations.generation_id`, `point_entries.operation_id` and the current diagnostics contract. The successful copy still checked one asset, one charge, one successful attempt and same-ID idempotent replay. Original-script failures and corrected-copy results are both retained; this documentation-only goal did not change the script.


## Target 10 executable isolated rehearsal

[`scripts/release-rehearsal.py`](../scripts/release-rehearsal.py) now provides a current-contract, dedicated-loopback-only rehearsal with newly owned databases, two workers, same-key replay, DB and local-media restoration and post-restore HTTP byte checks. It retains its synthetic databases/backups/media and stops its own processes. It does not supersede production approval, checksum authenticity, actual object-store/key recovery or RPO/RTO acceptance. See [the release review](RESOURCE_MARKETPLACE_RELEASE.md) for the recorded 384-table/5-file result and the NO-GO gates. The older worker-restart shell script remains unsuitable as a current release gate.
