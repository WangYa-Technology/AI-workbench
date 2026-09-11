# Production runtime baseline

`compose.production.yml` builds three non-root containers from this repository:

- `migrate`: idempotent embedded schema migrations;
- `api`: the Go HTTP process with a database readiness health check;
- `worker`: the durable job processor;
- `web`: a static Vue build that proxies `/api`, `/health`, and `/ready` to `api`.

It intentionally expects an externally managed PostgreSQL connection in `../.env.production`; it does not create a production database, credentials, certificates, payment account, or AI Provider authorization. Copy `.env.production.example`, place real values in the deployment secret manager, and make the generated file readable only by the deploy identity.

With the secret manager's environment injected, run the fail-closed application preflight before building or migrating:

```bash
make production-config-check
```

The preflight does not connect to PostgreSQL or external Providers. It rejects non-production mode, placeholder values, PostgreSQL connections without `sslmode=verify-full`, non-public or path-bearing `WEB_ORIGIN` values, unsafe local runtime flags, missing encryption keys, reuse of the same key across Webhook and identity-action encryption, and anything other than private S3-compatible storage plus an authenticated HTTPS scanner. Its output is a safe JSON summary of modes and counts only.

Once operations has approved an isolated staging bucket/prefix, least-privilege credentials, and the authenticated scanner, run the explicit external media acceptance command with the injected environment:

```bash
MEDIA_ACCEPTANCE_CONFIRM=I_APPROVE_MEDIA_ACCEPTANCE_CALLS make media-staging-check
```

The command is default-off, refuses local/deterministic adapters, creates only one random small text object, and removes it on success or failure. It proves create-only storage, stat, full and Range reads, SHA-256-bound authenticated scanning, deletion, and post-delete absence without printing the bucket, endpoint, object key, or credentials. Retain its safe JSON output with the release evidence. Run it only against an isolated staging prefix; it is not authorization to test a production bucket.

After that adapter check succeeds, run the separately confirmed application-level check against an approved disposable staging database while the same S3/Scanner environment remains injected:

```bash
MEDIA_APPLICATION_DATABASE_URL='postgres://staging-acceptance-database' \
MEDIA_APPLICATION_ACCEPTANCE_CONFIRM=I_APPROVE_MEDIA_APPLICATION_ACCEPTANCE_CALLS \
make media-application-staging-check
```

The wrapper refuses a preselected `search_path`, creates and later drops one random `hcai_media_acceptance_*` schema, and the Go command refuses any schema outside that namespace. It verifies HTTP registration/upload, pending isolation, durable scanning, clean full/Range delivery, cross-account denial, Job/audit/notification evidence, and object deletion without printing locations, keys, accounts, credentials, tokens, or response bodies. The command issues real storage/scanner requests and is never part of CI or normal startup. It does not validate the scanner's commercial policy quality, bucket lifecycle/encryption administration, outage recovery, backups, or infrastructure-level cross-account IAM.

Before a real deployment, require all of the following:

1. A TLS-terminating proxy that sanitizes forwarding headers and whose CIDRs exactly match `TRUSTED_PROXY_CIDRS`.
2. A PostgreSQL endpoint with verified TLS, backup/restore evidence, and a tested migration/rollback procedure.
3. Approved production integrations for email, storage/scanning, OAuth, AI Providers, and payments. Keep their supplied flags disabled until separately accepted.
4. Centralized logs, metrics, alerting, incident response, legal/privacy acceptance, and a manual accessibility/language release check.

The compose file is a runtime baseline, not production acceptance. API and Worker independently construct the same configured S3-compatible catalog and HTTP scanner contract; no shared media volume is used. The bucket must remain private. Asset delivery stays behind HCAI authorization and clean-scan checks, including bounded single-range streaming for video and audio. The adapter and application commands cover the disposable clean-object path only. Before public activation, separately verify bucket policy, encryption and lifecycle rules, review/rejected application transitions, scanner outage recovery, backup, and infrastructure-level cross-account isolation in staging.

When Waffo is enabled, deploy `services/waffo-connector` as a separate private
Node service reachable only by the API and Worker. Keep its rotated
`WAFFO_PRIVATE_KEY` and `WAFFO_CONNECTOR_TOKEN` in that service's secret
manager scope; do not add the private key to the API/Worker environment or the
web image. Set `WAFFO_CONNECTOR_URL` to the connector's authenticated HTTPS
endpoint in production. The base compose file intentionally does not start
this optional payment connector when Waffo is disabled.

Build and validate the Compose model without starting services:

```bash
docker compose --env-file .env.production -f deploy/compose.production.yml config
docker compose -f deploy/compose.production.yml build
```

For a syntax-only check with the checked-in placeholders, use `HCAI_PRODUCTION_ENV_FILE=.env.production.example docker compose -f deploy/compose.production.yml config` from the repository root.
