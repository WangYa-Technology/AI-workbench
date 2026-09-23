# Production runtime baseline

Billing Waffo callback checkpoint (0141): verified deliveries are bound to the immutable billing checkout request and committed dispatch digest, including original store/environment, buyer, external order, purpose, resource, amount and currency. Current sales settings no longer reject old obligations. The worker locks the payment and revalidates the binding before crediting funds or renewing a subscription. Drain old checkout writers and payment workers, apply migrations through 0141, then switch API and worker together; an old worker must not consume unbound events. Retain the original deployment verifier/environment for historical transactions. Legacy minimized events are not backfilled; an identical newly verified delivery may establish a binding only when its complete persisted tuple and original request match. Missing evidence returns reconciliation errors; no new billing quarantine/recovery UI is provided. Existing bindings or unprocessed billing events block downgrade. Owner exports now include an allowlisted binding summary. Unknown-outcome lookup, rejected billing evidence retention, Stripe billing callback review and real-provider acceptance remain open. See [0141 evidence](../docs/resource-marketplace-flows.md#11156-充值与套餐-waffo-原商户回调绑定0141). No running database or service was upgraded.

Wallet top-up settings (0136) add an audited, versioned singleton with a USD minimum and up to 12 unique suggested amounts. Defaults are a USD 0.50 minimum and USD 10/20/50/100/200 suggestions; amounts remain charged and credited equally in USD. The new INSERT guard locks these rules and rejects non-USD or out-of-range new wallet intents, including writes from older binaries. Existing accepted intents replay and fulfill using their original amount, even after the minimum increases. This does not add discounts, exchange rates, product-wallet purchases or seller settlement.

For rollout, stop old checkout/configuration writers, apply 0136 through the migration process, and ship the matching API and frontend with workers built from the same release. Verify authenticated settings reads, finance-only versioned saves, a stale-version rejection, changed-minimum refresh and original-intent replay in staging before allowing new payments. A 422 `wallet_topup_amount_out_of_range` refreshes the displayed limits without resubmitting Checkout; a 409 `payment_checkout_busy` means retry the original command. Pending top-up and subscription keys now use the shared finance command store: refresh and same-account reauthentication can recover an unacknowledged request within the same tab. Lost tab storage, another device and already-acknowledged Checkout sessions are outside this browser recovery guarantee; inspect original payment records before creating another payment.

Rollback of 0136 refuses once configuration has changed or a settings audit exists, including when values were later restored to defaults. Do not delete that evidence to force downgrade. The untouched default migration can roll back; otherwise use a forward correction and compatible application release. Current validation uses isolated schemas and simulated providers; running services and databases have not been upgraded. Current test evidence and remaining gates are tracked in [the marketplace handoff](../docs/resource-marketplace-journeys.md#92-当前工作区与已验证版本的差异).

Subsequent billing migrations 0137–0139 supersede the original dispatch gap described in the 0136 checkpoint. 0137 freezes the accepted checkout request and provider identity before a durable dispatch reservation; Waffo uncertain dispatch cannot be sent again, while Stripe retries require the original identity/request and a bounded window. 0138 freezes subscription terms and model membership and protects payment bindings; legacy missing contracts are not inferred from today's plan. Original-merchant callback binding, uncertain-outcome recovery and real-provider acceptance remain incomplete.

0139 prevents the same Waffo renewal payment from granting points or extending a period through a different webhook delivery ID. Deploy the matching API/worker after draining old billing writers and applying migrations in order. Unique ledger/event indexes reject duplicate renewal writes, including old-worker transactions. Existing duplicate ledger or renewal-event identities cause migration failure: investigate and reconcile the financial history rather than deleting it to force upgrade. Downgrade refuses when renewal evidence exists; use a forward correction. Empty-schema round trips are covered in isolated tests. The protection is scoped to the original local payment and provider payment ID, not a claim of verified cross-merchant binding. See [0139 evidence and remaining gates](../docs/resource-marketplace-flows.md#11154-同一笔续费的不同事件通知0139). Running services and databases have not been upgraded.

Manual refund-check requests now verify active `admin:finance` authority inside the Serializable command transaction and pin both the account and role grant before any write. Committed revocation during payment-lock waits aborts the command with 409; fresh unauthorized commands return 403. Later revocations wait for authorized commit, and audit failure rolls back prior-check updates, the new query/job and payment version together. Automatic reconciliation and previously authorized jobs continue under their existing rules. Rebuild API instances and the frontend error catalog (`payment_event_busy`); no new migration is required (0135 baseline). Current running services are unchanged. See [11.138](../docs/resource-marketplace-flows.md#11138-退款核对入队的财务权限与回滚) for isolated evidence and full-regression limits.

Product review now pins the active account and `admin:content` role grant through the listing read/decision and audit transaction. Conflicting obsolete snapshots return 409; fresh unauthorized requests return 403. Private review downloads retain the real reviewer, listing version and exact file selection throughout the asset handoff and HEAD/Open checks, in addition to existing owner/scan/locator checks. Rebuild and replace every API instance serving review endpoints; no schema migration is added (0135 baseline). Review audit records an authorized request, not proof of completed transfer. Already streamed bytes cannot be recalled. See [11.137](../docs/resource-marketplace-flows.md#11137-商品审核权限与私密文件下载交接) for isolated evidence and limits.

Delivery repair now rechecks and pins active `admin:media` authority after slow source/target verification and before reservation, target writes or ready-state commit. Commands explicitly use Read Committed; later revocations wait for an already-authorized transaction, and read-only inspection rechecks before returning success. A postcommit inspection can return 403 after a valid repair committed, so reconcile the original repair ID rather than assuming no mutation occurred. Rebuild and replace all serving API instances to apply this boundary; no new migration is added (0135 baseline). Isolated evidence does not cover real storage acceptance or deployment. See [11.136](../docs/resource-marketplace-flows.md#11136-交付修复期间撤权与文件写入权限).

Migration 0135 requires operator-scoped idempotency keys for manual wallet adjustments. Stop old API writers before migration and coordinate new API, frontend and data-export worker binaries. New database guards reject untracked adjustments from older binaries and roll back their balance changes; do not suppress this failure or retry with fresh keys. Command, ledger and audit commit atomically. A replay returns the original operation ID and the account observed in that authorized request, not a frozen historical balance. Pending browser keys survive same-tab reload/reauthentication until acknowledged success; lost tab storage or another device requires ledger review rather than an assumption of automatic deduplication. Historical entries are preserved without fabricated commands; downgrade refuses once any command exists. Only isolated schemas were migrated. This is wallet bookkeeping, not seller settlement or external transfer acceptance. See [scope and tests](../docs/resource-marketplace-flows.md#11135-手工额度调整幂等与重试链路0135).

The recovery-authority update requires rebuilt application binaries but no new schema migration (baseline remains 0134). Media cleanup, data export and account-deletion recovery bind active operator permission to the enqueue statement; request recovery explicitly uses Read Committed so configured stronger defaults do not reuse stale permission snapshots. This does not cancel previously authorized jobs or bypass worker retention checks. Rebuild images from the final source before release; older local image verification does not cover subsequent application changes. See [11.131](../docs/resource-marketplace-flows.md#11131-恢复入队时的运营权限与支付回归闭合).

Finance webhook review additionally locks active operator authority inside its Serializable transaction. Webhook ingress and operator review share a nonblocking transaction lock per provider/event ID; ingress contention returns HTTP 503 `payment_event_busy` with `retryable=true`, while review contention returns 409. Preserve provider retries of the original signed delivery and do not translate these failures into successful acknowledgement. All API instances serving either entry point must run the matching implementation before claiming the lock-order guarantee; drain older instances during cutover. No new migration is required, and current local runtime images predate this change. See [11.132](../docs/resource-marketplace-flows.md#11132-财务回调复核的权限快照与事务保护) for isolated evidence and remaining authority-audit scope.

Payment recovery and provider-event replay now check finance authority in the domain transaction and pin the active account/permission rows before enqueue. A committed revocation invalidates the prior Serializable snapshot; the command rolls back with 409, while fresh unauthorized commands return 403. Later revocations wait for an already-authorized transaction to finish. Replay also writes actor/event/payment/request/version audit attribution atomically with its processing changes and job. Rebuild and replace API instances; this adds no schema migration and does not cancel previously authorized jobs. Other finance configuration and delivery-repair authority paths remain separate acceptance work. See [11.133](../docs/resource-marketplace-flows.md#11133-财务恢复事件重放的撤权与操作者审计).

Finance settings now use the same transaction-level authority guard: payment-provider configuration, payout-destination changes and manual balance adjustments pin active operator permission before commit, with atomic audit attribution. Provider updates serialize all switches before reading and merging partial fields, explicitly using Read Committed even on pools with stronger defaults. Keep the existing single-enabled-provider unique index. Replace all API instances together to establish the shared configuration lock protocol; old instances do not participate. This adds no migration (0134 baseline), makes no provider calls and does not make manual balance adjustments retry-safe. Rebuild images from current source; isolated evidence and outstanding scope are in [11.134](../docs/resource-marketplace-flows.md#11134-财务设置撤权审计原子性与配置并发).

Runtime builds use BuildKit's `BUILDPLATFORM`, `TARGETOS` and `TARGETARCH`: the compiler runs on the build host and produces the requested target executable. Do not hardcode `TARGETARCH=amd64`; that previously produced ARM64 images containing x86-64 binaries. `make container-build` now builds the migration image as well as API, worker and web, and checks all three Go image executables. For prepared release tags, run `make runtime-image-check RUNTIME_IMAGES='your-api:tag your-worker:tag your-migrate:tag'`. The checker compares image metadata with the copied ELF header using unstarted, network-disabled temporary containers; it does not start the application or run migrations. CI builds and checks both Linux AMD64 and ARM64 targets. This architecture check is separate from service startup, migration, provider and deployment acceptance.

The web runtime baseline is now `nginx:1.30.5-alpine`. The previous 1.27.5 image falls within the published affected range for [CVE-2026-42533](https://nginx.org/en/security_advisories.html), and the checked-in configuration uses regular expressions inside `map`. Rebuild the web image; changing the Dockerfile alone does not patch running containers. The proxy test verifies the executable's exact patch version against the Dockerfile before running its transport tests, rejecting stale local images. Local fixed-image and negative-control evidence is recorded in [11.129](../docs/resource-marketplace-flows.md#11129-运行镜像架构代理版本与指标查询).

`compose.production.yml` builds four non-root containers from this repository:

- `migrate`: idempotent embedded schema migrations;
- `api`: the Go HTTP process with a database readiness health check;
- `worker`: the durable job processor;
- `web`: a static Vue build that proxies `/api`, `/health`, and `/ready` to `api`.

The Go build baseline is 1.26.8 in `go.mod`, CI and `Dockerfile.runtime`. Rebuild all Go executables when applying the dependency security update; changing source manifests does not patch already running binaries. Before release, run `make security` with access to the official vulnerability services. It rejects known vulnerabilities in imported Go packages and production npm lockfile dependencies; application tests and real-provider acceptance remain separate gates. See [dependency scope and evidence](../docs/resource-marketplace-flows.md#11104-生产依赖漏洞修复与-ci-检查).

The non-root web container needs explicitly owned tmpfs mounts: `/var/cache/nginx` and `/var/run` use mode 0700 and UID/GID 101, while `/tmp` uses mode 1777. A default root-owned mount hides the image-layer ownership and prevents Nginx from creating `client_temp`. The web Dockerfile checks the nginx UID/GID at build time; retain the matching Compose ownership when updating the image.

Only `/api/v1/assets/uploads` and `/api/v1/assets/{assetID}/versions` receive a 10496 KiB request envelope (10 MiB file plus 256 KiB multipart overhead). Requests stream to the API without proxy request-body buffering; the API still authenticates, validates the idempotency key and enforces its own file/envelope limits. Other API routes retain their default proxy limit. Delivery-repair uploads keep their separate 100 MiB limit.

Validate this transport boundary after preparing the local images:

```bash
docker build -f deploy/Dockerfile.web -t hcai-chat-web:local .
docker pull node:24-alpine
make proxy-test
```

`make proxy-test` reads the checked-in Compose web mounts/security options and Nginx configuration, then creates a temporary network and two disposable containers. Its host port is bound only to loopback. The Node upstream hashes received bytes; no application, database, account, storage/scanner or payment service runs. The test checks non-root/read-only startup, full-size ordinary and chunked uploads, over-limit rejection, unchanged unrelated-route limits and the separate repair limit. It removes its own containers/network on completion and never pulls images implicitly. `HCAI_PROXY_TEST_IMAGE` and `HCAI_PROXY_FIXTURE_IMAGE` select prepared images. CI builds the web image and runs this check independently. This transport test does not replace actual API authorization, scanning, full container-build or production integration acceptance.

The proxy also uses query-free JSON access results and generated upstream request IDs. Raw HTTP request diagnostics are suppressed because they can expose email-action tokens; startup diagnostics remain. Static pages send `Referrer-Policy: no-referrer`. The proxy test covers successful, oversized and failed-upstream requests with synthetic private markers; see [logging fields and operational tradeoffs](OBSERVABILITY.md#http-proxy-logs-and-temporary-credentials). Adapt the release log collector and external TLS proxy separately before deployment.

For registration and login network evidence, the API walks all `X-Forwarded-For` fields from right to left, starting with the TCP peer. It proceeds only while each current hop belongs to `TRUSTED_PROXY_CIDRS`, and stops at the first untrusted address. The checked-in Nginx appends its observed peer with `$proxy_add_x_forwarded_for`; caller-supplied prefixes therefore remain untrusted. Configure only the actual proxy networks, including the immediate Web container and any trusted outer ingress; do not include ordinary client networks. Each trusted proxy must append the peer it observed or replace the header with a verified chain. If the direct peer is untrusted, forwarding headers are ignored; a malformed hop inside the trusted suffix falls back to the direct peer instead of skipping to a caller-controlled prefix. IPv4-mapped addresses are normalized before hashing. `X-Real-IP` and `Forwarded` are not alternative identity sources. No raw client IP is added to session/risk storage or proxy logs. See [reproduction and verification](../docs/resource-marketplace-flows.md#11109-登录交接中的代理链与账户关联证据).

Auth-code requests now coordinate the existing 30-second resend window across API instances using a nonblocking transaction advisory lock scoped to normalized email and purpose. Concurrent requests return 429 with `Retry-After: 30` and `auth_code_resend_limited`; accepted requests still only queue delivery. No migration is required, but replace every old API instance because old processes do not participate in this lock protocol. Ship the matching frontend error messages and OpenAPI client. This does not provide cross-recipient anti-abuse limits or prove SMTP inbox delivery; see [verification](../docs/resource-marketplace-flows.md#11110-验证码申请的跨实例并发与重发提示).

New auth-code digests are HMACs bound to the challenge ID and purpose, using a domain-separated verification key derived from `EMAIL_ACTION_ENCRYPTION_KEY_B64`. Keep this original key stable and identical across API and worker; delivery ciphertext is unchanged. Replace all old API instances before accepting new challenges because old verification code cannot read the new digest. Already-issued legacy codes retain their original expiry and attempt limits, and no database migration or historical hash rewrite is performed. See [compatibility and test scope](../docs/resource-marketplace-flows.md#11111-验证码摘要的申请绑定与旧格式兼容).

Browser writes now pass Go's `CrossOriginProtection` before business handlers. Set `WEB_ORIGIN` to the actual frontend origin; its optional trailing slash is normalized consistently with CORS. Preserve `Origin` and `Sec-Fetch-Site` through every ingress—do not strip or replace them with trusted values. Same-site sibling origins are not automatically trusted. Rejected requests return 403 `cross_origin_request`; GET/HEAD/OPTIONS remain safe, and non-browser clients without these headers retain normal authentication requirements. Only exact POST Stripe/Waffo callback paths bypass browser-origin checks, retaining their signed-payload validation. This requires the updated API and matching frontend message, without a migration. See [browser, transaction and proxy verification](../docs/resource-marketplace-flows.md#11112-浏览器写操作的跨来源保护).

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

Current worker monitoring requires migration 0109 for periodic maintenance health and 0110 for the running-lease expiry index, alongside matching API/worker/check-script versions. The 0110 index uses ordinary `CREATE INDEX`; assess write-lock duration before rollout. Recovery remains active at full worker concurrency, and expired owners cannot renew or finalize a job. Invalid execution evidence is retained and alerts rather than being silently repaired. See [lease recovery and operational response](OBSERVABILITY.md#running-job-leases-0110). These changes have not migrated the running development database or completed production acceptance.

The compose file is a runtime baseline, not production acceptance. API and Worker independently construct the same configured S3-compatible catalog and HTTP scanner contract; no shared media volume is used. The bucket must remain private. Asset delivery stays behind HCAI authorization and clean-scan checks, including bounded single-range streaming for video and audio. The adapter and application commands cover the disposable clean-object path only. Before public activation, separately verify bucket policy, encryption and lifecycle rules, review/rejected application transitions, scanner outage recovery, backup, and infrastructure-level cross-account isolation in staging.

The S3 adapter rejects HTTP redirects, including redirects within the same origin. Configure the correct bucket region and endpoint directly; redirect-based endpoint discovery is not supported. Full object reads require 200 and a valid Content-Length without Content-Range; ranged reads require 206 with matching range bounds, total size and declared Content-Length. Before streaming asset content, the API rejects changes in object size or a previously supplied ETag between Stat and Open. Purchased snapshots still undergo full SHA-256 verification. Compatible storage gateways must preserve these headers; verify them with the explicit staging acceptance commands above. Local protocol regression is recorded in [the marketplace audit](../docs/resource-marketplace-flows.md#610-s3-请求边界分段读取与对象变化保护); it does not prove live bucket, cache, backup or IAM acceptance.

Local development storage and any explicitly provisioned historical `local_file` roots require Unix directory-relative filesystem operations. Object keys must resolve to regular files with exactly one hard link; symlinks, dangling links and special files are rejected for metadata, reads and cleanup. Protect the configured root and its parents from untrusted modification. Existing aliases or two-link staging objects left by a crash require evidence-based operator handling; do not delete unknown links automatically to make a job succeed. No new database migration is needed, but API and worker should use the same storage implementation. This does not add historical local files to the production Compose deployment or replace the S3 requirement; see [local storage scope](../docs/resource-marketplace-flows.md#611-本地媒体的文件链接与目录边界).

Object absence now requires an accessible storage location. An unavailable local root fails reads and cleanup instead of reporting an absent file. On an S3 object-operation 404, the adapter performs an uncached, signed `HeadBucket` with the same endpoint, bucket and credentials; an inaccessible bucket cannot produce an object-missing result or a verified deletion receipt. Permit `HeadBucket` on the configured private bucket (AWS S3 requires `s3:ListBucket` on that bucket ARN), and verify equivalent behavior for compatible providers. No object listing or public access is added. Restore the actual storage location or required access before retrying cleanup; do not create an empty replacement root to satisfy a cleanup check. Deploy matching API/worker binaries; no migration is added. These checks do not prove mount identity, object-version removal, backup erasure or correctness of changed storage configuration. See [outage and recovery evidence](../docs/resource-marketplace-flows.md#11113-存储位置不可用不能作为文件删除证明).

Each local Store also remembers the filesystem identity of an existing root at construction, or of a previously uninitialized development root on first use. Later operations reject a different directory, and writes cannot recreate an observed root after it disappears. Restoring the original directory permits the same Store and cleanup job to continue. This in-memory guard is not a cross-process or persistent mount identity: provision and verify historical local roots before constructing services, and validate original media evidence before any intentional replacement or process restart. A new Store constructed against an already replaced directory cannot infer its history. No reset/adopt action is exposed; see [directory replacement verification](../docs/resource-marketplace-flows.md#11114-本地目录替换与写入自动重建的保护).

Product delivery cleanup and account original-media cleanup both verify the same primary location after Delete. The cleanup credential must be able to distinguish a missing object from forbidden/unavailable metadata. A successful Delete response alone cannot mark delivery snapshots or repair objects removed, nor complete account-deletion receipts; unresolved reads remain retryable jobs. Historical removed rows, previous storage configurations, provider versions, backups and caches require separate reconciliation. See [cleanup verification scope](../docs/resource-marketplace-flows.md#612-副本清理必须核验主存储定位已不存在).

The operator delivery evidence inventory at `/admin/deliveries` uses migration 0111's global order-creation index. It checks at most 500 orders per page, with one lookahead; empty matching results can still have a continuation cursor. Deploy the matching API, generated frontend contract and UI together. The index is an ordinary transactional CREATE INDEX: assess write-lock duration on large order tables. This migration adds no acceptance evidence and does not migrate historical files. The running development database has not been upgraded. See [inventory scope](../docs/resource-marketplace-flows.md#614-历史交付证据核对入口0111).

## Stripe test-object acceptance

`cmd/stripecheck` is a separately approved staging check, not part of deployment startup or CI. With the approved staging environment injected by the secret manager, the explicit command is:

```bash
STRIPE_ACCEPTANCE_CONFIRM=I_APPROVE_STRIPE_STAGING_CALLS \
STRIPE_ACCEPTANCE_MAX_CALLS=6 \
go run ./cmd/stripecheck
```

The existing guards require staging, enabled Stripe test mode, live mode and its approval disabled, a test secret and Webhook secret, the official API base URL, and a public HTTPS Web origin. Never put credentials into this command or use it to validate production/live payments. It first authenticates test mode through `GET /v1/balance`, then creates an unpaid Checkout Session, expires it, creates a disposable Connect account and onboarding link, and deletes that account. Mode verification failure stops before any writes. It does not follow the link, confirm a payment, exercise a signed Webhook, deliver a purchased product, refund a charge, or settle seller income.

Each attempted operation reserves one of six request slots, including the mode read and failure cleanup. Acceptance uses an isolated standard HTTP transport with fresh HTTP/1 connections and no redirects, preventing transparent replay on reused connections; shared application pools and TLS trust remain unchanged. Unsupported custom RoundTrippers are refused before dispatch. The budget covers application requests dispatched by this command, not DNS/TLS/proxy handshakes or independent proxy retries. A local failure can consume a slot without reaching Stripe. The old five-slot opt-in no longer starts this command; operators must explicitly opt into the new six-slot budget.

Once acceptance starts, retain stdout JSON even when the exit code is nonzero. `requestCount` reports consumed slots. `cleanupComplete` is false when `pendingCleanup` contains unresolved test-object IDs or `unconfirmedCreations` contains the original creation idempotency key. A failed sixth request does not trigger a seventh request. Failure cleanup has a separate ten-second deadline, so caller cancellation neither skips all cleanup nor leaves it unbounded. Preflight rejection may exit before a result is created.

For a failure, use the original test merchant and request/object references to establish what happened. An unconfirmed creation is not proof that nothing was created; do not rerun the entire check as a cleanup mechanism. Verify object state in the test dashboard and arrange separately approved cleanup if required. Keep these operational references in controlled release evidence; output excludes secret keys, Checkout/onboarding URLs and raw Provider bodies. This command has only loopback regression evidence for the latest changes; real merchant and application acceptance remains pending. See [scope and regression evidence](../docs/resource-marketplace-flows.md#1185-stripe-测试验收的请求预算与清理证据).

## Verified product media staging

Order-copy preparation, purchased downloads and delivery repair share a process-wide staging budget across independently constructed media catalogs. `MEDIA_STAGE_MAX_BYTES` defaults to 512 MiB (environment range 256 MiB–8 GiB), `MEDIA_STAGE_MAX_OBJECTS` to 16 (2–128), and `MEDIA_STAGE_MIN_FREE_BYTES` to 64 MiB (1 MiB–1 TiB). Each file reserves its maximum read size plus one oversize-probe byte until Close, including the lifetime of a download response. Source and destination verification may overlap during repair.

`MEDIA_STAGE_TEMP_DIR` must be an existing writable absolute directory when set; otherwise the OS temporary directory is used. Compose overrides it to `/run/hcai-media-stage` and supplies a private 768 MiB tmpfs for **each** API/worker container, mode 0700 and UID/GID 10001. This is separate from the 1280 MiB export mount and 256 MiB `/tmp`. Their combined maximum is 2304 MiB per container, before application and other memory. tmpfs consumes memory on use, not preallocated disk. Size host/container memory and mount capacity together, including all replicas; increasing configuration limits does not resize the mount.

Admission and write-time free-space checks fail closed. Resource failures expose retryable `media_stage_busy` or `media_stage_storage_unavailable` errors (HTTP 503, Retry-After 5); existing-order preparation retains its original-order recovery response. Paid fulfillment retries the original job without premature rights or capacity-only refunds. Directory/storage failures must be repaired before retrying. These limits are not cluster-wide reservations, per-user fairness, or an external connection budget. Slow consumers can retain slots; production concurrency and timeouts require load acceptance.

No new database migration is required for this staging change. Ship matching API/worker configuration and frontend error messages. The running services have not been restarted, and production storage/capacity acceptance remains pending. See [behavior and regression evidence](../docs/resource-marketplace-flows.md#613-交付校验的共享暂存预算).

Asset-content and product-review downloads now stream through Nginx without response buffering, proxy caching or proxy temp-file spooling. Client backpressure therefore reaches the API instead of releasing its verified-file reservation while Nginx retains a second full private copy. The shared API media writer establishes a seven-minute absolute deadline for verification and transfer after content authorization, with a matching context deadline and `X-Accel-Buffering: no`. Normal JSON response deadlines remain unchanged. Nginx allows 420 seconds between upstream reads and 30 seconds of downstream write inactivity; these idle limits do not replace the API's absolute budget. Cancellation closes the upstream and the API still closes its media reader.

Roll out the matching API and Web proxy configuration together. Disabling buffering while retaining the old API's 30-second ordinary response deadline can truncate valid slow downloads. Preserve Range/Content-Range, ETag, Content-Length and private/no-store metadata when adding an external proxy. The disposable proxy test exercises 64 MiB responses, stalled-reader backpressure, absence of proxy temp files, cancellation and Range transport; API integration tests separately check purchased ZIP/member access and deadlines. This is not a multi-instance production load or real-storage acceptance result; see [download evidence](../docs/resource-marketplace-flows.md#11108-私密下载的代理背压与媒体响应时限).

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

## Bundle publication cutover (0115)

Migration 0115 enables reviewed multi-file publication and adds bundle-aware writer and worker checks. Deploy it with the current API, worker and frontend, after migrations 0112–0114. New database connections declare `app.product_bundle_protocol=zip-v1`; this is a binary capability declaration, not authentication. Old connections cannot write accepted bundles or claim/renew/finish running jobs. It does not prevent old API read paths or cancel external IO already in progress.

For this release, stop routing requests to the old API, stop old periodic dispatch, and drain/stop every old worker before migration. The migration locks `jobs` against concurrent writes and refuses any `running` row. Investigate expired leases through the existing recovery evidence before retrying; never mark a job successful, cancel a financial operation, or delete a row solely to pass migration. Then migrate, start only the compatible API/worker, verify readiness and worker execution, and switch the frontend/traffic together. Do not use a rolling deployment mixing old and new binaries or manually add the protocol flag to an old binary.

The down migration refuses any accepted bundle contract, bundle submission/approval history, or active bundle. After such evidence exists, recover forward; do not erase contracts or histories to roll back. Database enforcement preserves complete ZIP asset shape and entitlement identities but does not replace real merchant, scanner, private object-store, resource-capacity, or recovery acceptance. The development running database has not been upgraded by the isolated tests.

## Generation output journal cutover (0116)

The current API and worker also require migration 0116 and declare `app.generation_output_protocol=journal-v1` on every database connection. Deploy the migration, API, worker and metrics check script together; do not merely set the declaration on an older binary. As with 0115, stop old API traffic and all old workers, drain outstanding external operations, and resolve running-job evidence before migration. A database flag cannot recall an already issued provider or storage request.

The journal records output locations before writing files and attaches them in the same transaction as successful generation and billing. The new worker runs independent cleanup maintenance; account deletion checks uncommitted locations before completing. Verify the seventh maintenance kind, cleanup error/due gauges, normal media generation, and a non-production fault-recovery exercise before restoring traffic. Full behavior, alert limits and remaining acceptance requirements are documented in [Generation output journal and cleanup](OBSERVABILITY.md#generation-output-journal-and-cleanup-0116).

Rollback is allowed only with an empty journal and no running jobs. Once any intent exists, recover forward instead of removing storage evidence. Neither 0116 nor its tests inventories older orphaned objects, proves production storage/backup erasure, or validates real supplier billing. The running development database has not been upgraded by these isolated tests.

### Generation execution recovery cutover (0117)

Drain running jobs and stop old API/worker instances before applying 0117. The migration refuses running jobs; new connections declare `app.generation_execution_protocol=lease-v1`. Old generation writers and job claimers are rejected. Do not downgrade after execution bindings exist: they are durable evidence, not disposable cache.

`generation_executions` binds each new generation to its actual queue job. Historical backfill uses only a sole matching `generation.generate` job with canonical generation ID; missing or ambiguous matches remain unbound. Inventory `hcai_generation_recovery_unresolved` and investigate original records before enabling a historical task. Do not make up a job binding or clear a reservation solely because a task is old.

On startup and every minute, the worker independently reconciles up to 100 evidenced terminal failures within 10 seconds. It atomically fails unfinished generations, releases point and legacy currency reservations, and queues failure evidence. Valid completed/cancelled generations are excluded. Current lease tokens and attempts are checked before dispatch and before result commit; this cannot recall an already-dispatched Provider request or guarantee a single external charge.

Upgrade acceptance must include both `generation_execution_recovery` maintenance health and the recovery backlog metrics described in OBSERVABILITY.md. An inconsistent balance must raise an error and preserve all prior states for investigation, not be silently marked released. Isolated schema tests do not constitute applying this migration to any running environment.

## HTTP scanner response boundary

Deploy the updated API/worker scanner together with asset scan finalization. A
scanner URL must be its final endpoint: redirects are rejected without forwarding
private bytes, object identities or authorization. Responses must be complete,
valid and at most 16 KiB after decompression. The configured timeout covers the
body as well as headers. Body timeouts and transport failures remain retryable;
permanent scanner errors move the asset to controlled review, preventing files
from remaining indefinitely pending after their job stops retrying. A review
state does not grant clean status or market publication rights.

No new migration is needed. This change does not automatically requeue historical
failed scans or verify malware detection quality. Stage with the real final
endpoint, private storage and scanner policy before production use. Isolated
regression scope is recorded in [marketplace scanner boundaries](../docs/resource-marketplace-flows.md#626-扫描服务的请求响应与失败收尾)
and detailed record 11.88.

## Waffo connector HTTP outcomes and cancellation

Deploy matching `server.mjs` and `transport.mjs` with the existing pinned SDK.
The transport rejects failed HTTP responses before SDK parsing, preserves 429
alongside 401/403 in outward error classification, and cancels remaining parallel
SDK reads when the incoming request finishes. Successful responses retain the
existing decoded-body limit and deadline. No new migration or financial request
contract version is introduced by this change.

Keep checkout/refund one-shot reservations after uncertain outcomes; cancelling
a read cannot undo an already accepted financial operation. No authenticated
Waffo transaction query is introduced. Local SDK and isolated Go tests do not
replace actual-provider acceptance, and running services have not been changed.
See [verification and remaining boundaries](../docs/resource-marketplace-flows.md#11115-waffo-失败响应与请求结束后的并行取消).

## Waffo checkout dispatch (0120)

Ship migration 0120 with the matching API, worker and frontend. Stop old checkout writers before cutover: the existing `guarded_v1` request and immutable dispatch records now permit only the invocation that first reserved dispatch to create the remote session. Do not mix old API processes with the new deployment. SDK 0.19.1 rotates checkout idempotency keys across 60-second buckets; original local IDs and frozen payloads alone do not guarantee safe replay.

A lost response, committed reservation followed by a crash, legacy/missing request evidence or inconsistent pending remote identity requires reconciliation. Neither a new command key nor a restarted process receives another dispatch permit. The original caller rechecks the payment version, request digest and independent lookup/quarantine holds after reservation commit and lock acquisition. A valid saved URL remains reusable; verified late payment results still enter ordinary fulfillment/compensation.

`product_waffo_checkout_review` feeds the shared review policy, buyer `checkoutReconciliationRequired`, seller `needsReview` and finance attention. The in-flight first request can briefly project as needing review while awaiting its result. Pending delivery remains retained and local closure is unavailable. There is no authenticated Waffo lookup implementation yet: do not reset records, close uncertain orders, or route Waffo through Stripe recovery buttons. Existing checkout age/backlog monitoring remains relevant.

The down migration refuses while unresolved Waffo checkout review exists. These changes have only been applied to isolated test schemas; no running database, actual service or merchant has been changed. Rules and verification: [checkout dispatch](../docs/resource-marketplace-flows.md#517-waffo-创建会话的一次性派发0120), detailed record 11.87.

## Refund-query execution ownership

Deploy matching API/worker code and replace all old refund-query workers. The
failure writer now checks the original job state, attempt number, lease token
and query phase under database locks. It rechecks lease expiry after waiting for
both the job and query row. A stale execution cannot fail its replacement's
query, and errors saving a failure remain retryable instead of ending the job
without a durable state change. No external requests occur under these locks.

The original expired-lease history stays visible until ordinary job completion;
do not clear it as a workaround. No migration or API field is introduced. Only
isolated database schemas and simulated payment reads were exercised; running
services remain unchanged. See [reproduction and verification](../docs/resource-marketplace-flows.md#11117-退款查询迟到失败与任务租约保护).

## Incomplete Stripe refund queries

Ship matching API and worker code for refund-query evidence preservation. The
Stripe reader now returns individually verified observations alongside later
pagination errors. The worker stores them as an immutable failed check with the
safe error code, payment version and audit in one transaction. It does not apply
refund events from this incomplete result or retry the same check over new data;
operators request a new read. Existing observation-review and retention rules
keep unmatched refunds visible after later empty results.

No new schema or API field is introduced; existing migrations 0125/0126 remain
required. Replace old workers that discard partial reads. Cancellation uses a
separate five-second evidence-save budget, without additional provider requests;
this cannot guarantee persistence during database outages or forced process loss.
Only isolated schemas and loopback Stripe fixtures have been used; running
services and real providers have not changed. See [evidence and recovery](../docs/resource-marketplace-flows.md#11116-stripe-退款分页中断的已验证证据保留).

## Stripe refund retry window (0119)

Ship migration 0119 with the matching API and worker. It extends the shared refund review view: a requested/pending Stripe operation without a recorded remote refund ID requires reconciliation once its immutable request time reaches 23 hours, is in the future, or is missing/inconsistent on the current order; a changed refund-correlation protocol also requires review. The worker rechecks the operation with the database wall clock after merchant verification, immediately before financial dispatch. This window limits the original refund command; it does not shorten the buyer's contractual refund-request period or use the purchase date as a dispatch deadline.

Stop old refund writers before cutover. Do not reset an operation timestamp, change its ID, or requeue it to bypass the gate. The existing finance entry shows `refund_reconciliation_required` and refuses direct refund recovery; use the original merchant's authenticated refund query. Once the dispatch job records its non-retryable failure, the existing periodic scanner can enqueue that read. Verified success revokes rights through the existing handler; verified failure permits a fresh request under the original policy. Missing or contradictory results remain unresolved. Waffo's one-shot reservation remains unchanged.

The migration changes a view without fabricating past dispatch evidence. Its down migration refuses while any unresolved Stripe operation lacks a remote refund ID, including one still within its window. Preserve original attempts and query evidence when restoring backups. Existing unresolved-refund age and due-query metrics continue to cover these obligations. Only isolated-schema tests have applied this migration; the running database and real services have not been upgraded. See [business and recovery rules](../docs/resource-marketplace-flows.md#78-stripe-未确认退款的派发时限0119) and [regression scope](../docs/resource-marketplace-flows.md#1186-stripe-退款派发窗口与原交易查询恢复).

## Rejected product webhook evidence cutover (0118)

Coordinate migration 0118 with the API, worker, finance UI and alert checker. Stop old writers and cleanup workers before changing traffic: the new callback admission, refund reservation recheck, account export and media retention paths depend on the quarantine objects. Do not run an old worker against the new review rules. This repository's isolated schema tests do not apply the migration to a running development or production environment.

Only signature-verified and fully normalized product events rejected by local transaction checks enter `product_webhook_quarantines`. Invalid signatures, malformed envelopes and incompatible versions rejected before normalization are outside this replayable evidence store. Raw request bodies, signatures, credentials, email and arbitrary metadata are not retained. Pending records matched to an original remote transaction hold repeat checkout, refund dispatch and relevant copy/account cleanup; a claimed local UUID alone does not establish a match.

Finance operators use the evidence section in `/admin?tab=finance`. Rechecking requires the current version and a reason. It validates the immutable receipt without contacting a provider; `admitted` means entry into ordinary event processing, not paid, refunded or delivered. There is no manual ignore/resolve override. Persistent contradictory evidence requires trustworthy provider reconciliation; Waffo active querying and the complete financial reconciliation workflow remain outstanding.

Verify the two quarantine series and alerts described in [observability](OBSERVABILITY.md#rejected-product-webhook-evidence-0118). Preserve the immutable receipts and checks during backup/restore. Down migration refuses when receipts exist. Buyer exports include only evidence associated through an original matched payment or an admitted event, exclude private operator reasons, and do not attribute an unverified local-ID claim to a buyer. Final retention periods and production restore/load acceptance still require explicit policy and evidence.


## Asset scan execution recovery (0121)

Scan and generation recovery now lock their execution binding before trying account/entity/job locks. A concurrent maintenance pass skips the binding instead of postponing work another process is already recovering. Subsequent lifecycle and entity locks remain nonblocking, preserving progress past busy accounts and jobs. This requires updated API/worker binaries but no additional migration; exact-interleaving and lock-fairness evidence is recorded in [11.103](../docs/resource-marketplace-flows.md#11103-扫描与生成恢复不能互相推迟在途执行).

Stop old API/upload writers and drain workers before migration 0121. The migration rejects running jobs, binds only historical uploads with exactly one matching scan job, and protects those bindings and job identities. New connections declare `app.asset_scan_execution_protocol=lease-v1`; old upload writers and old workers claiming/updating running jobs are rejected. Ship the API, worker, migration and metrics alert checker together. Evidence-bearing bindings or running jobs prevent rollback.

The scanner verifies the bound job and durable running attempt before storage access, again before external scanning, and under asset/job locks before saving results. Attempt counters come from the database; lease deadlines are checked after lock waits. External I/O holds no job lock, so heartbeats remain available. Scan finalization takes the account lifecycle lock; deleted owners receive no new scan notifications or clean verdicts.

A startup/minutely maintenance pass (100 candidates, ten-second budget) converts pending uploads to review only when the original failed job has matching finished-attempt evidence. Exhausted lease expiry is supported; queued retries, missing/ambiguous evidence and prior manual decisions are not overridden. Audit, notification and state commit together. Failures roll back and back off for one minute; `asset_scan_execution_recovery` participates in shared maintenance health and alert checks. A healthy pass does not prove every historical pending upload was resolved; investigate unbound uploads through media operations.

The accompanying API/checker update also reports pending scan age, due recovery age, failed checks and unresolved execution evidence independently of healthy maintenance passes. Defaults are 900 seconds for pending uploads and 300 seconds for due recovery; see [scan monitoring](OBSERVABILITY.md#scan-business-backlog-and-evidence-gaps).

Only isolated test schemas have applied this migration. No live service, actual scanner or running database was changed. See [rules and verification](../docs/resource-marketplace-flows.md#627-扫描执行绑定租约复核与中断恢复0121).


## Upload commit acknowledgement and account lifecycle

Deploy the API change with its generated upload contract. Uploads now validate active account status and version ownership before Put while holding lifecycle/account locks; account deletion cannot finish between this validation and asset insertion. Lost commit replies are verified using a fresh serialized database read. A matching original asset is returned; unavailable or conflicting verification preserves bytes and reports failure. Verification and definite-failure cleanup each have five-second bounds. Revocation after authentication returns HTTP 403.

This needs no additional migration beyond the existing baseline, and no running service was changed. That original checkpoint did not include durable upload intents or client retry idempotency; subsequent 0122 and 0123 cutovers below cover those additions. Unknown object reconciliation remains unfinished. Do not interpret preserved bytes after an uncertain result as proof that metadata committed, or delete them solely because the caller saw an error. See [upload scope](../docs/resource-marketplace-flows.md#630-商品来源上传的提交响应丢失与账户状态复核).

## Upload write journal cutover (0122)

The current API and worker require migration 0122 and declare `app.upload_write_protocol=journal-v1` through `database.Open`. Ship the migration, API, worker and metrics checker together. Stop old API/upload traffic and workers and drain outstanding external writes first; the SQL guard cannot recall already-issued storage requests. Running jobs block migration and downgrade. Do not add the protocol flag to an older binary to bypass this protection.

Upload ownership and immutable digest/size are now committed before storage writes; asset creation, scan binding, audit and journal attachment commit together. Failed requests and periodic maintenance clean only verified, unbound locations without holds or references. Account deletion includes unbound uploads before issuing its completion receipt. New object keys use the journal ID; operational tools must read recorded locations rather than infer them from asset IDs.

Verify an ordinary upload and scan, an isolated crash/recovery exercise, account export/deletion, the `upload_write_cleanup` heartbeat and all three [business cleanup metrics](OBSERVABILITY.md#upload-write-journal-and-cleanup-0122) before restoring traffic. Any journal row prevents downgrade; retain evidence and recover forward. Historical upload rows are not given invented journals. Client retry idempotency is covered by 0123 below. Unknown historical objects, provider versions/backups and production capacity remain outside this checkpoint. Migration 0122 has only been exercised in isolated test schemas; the running environment has not been upgraded.

## Upload command cutover (0123)

Ship migration 0123, API, worker, frontend and generated API contract together after stopping old upload traffic, draining workers and outstanding storage writes. Connections declare `app.upload_command_protocol=command-v1`; do not add this flag to old binaries to bypass the guard. Running jobs block migration/rollback, and any command/attempt evidence blocks downgrade. The original 0122 journal remains required.

Both upload routes now require exactly one valid `Idempotency-Key` (8–128 ASCII letters/digits or `._:-`). Missing/invalid headers return 422; changed content under the same account/key returns 409. First completion returns 201, recovery returns 200 with the same asset ID and current scan state. The server binds normalized metadata, actual bytes, and version target/note; it commits the result with asset/scan/audit/journal attachment. Old clients receive an explicit validation error instead of silently creating an unprotected upload.

The shared browser client retains opaque retry keys and hashes across network errors and same-tab refresh, coalesces matching in-flight calls and fences account changes. Re-selecting the same file and fields recovers an uncertain result. Successful requests end the local retry record; intentionally new keys represent new uploads. Cleared/unavailable browser storage or a different tab does not imply the same operation. External callers must retain their original key rather than generate a fresh key on retry.

Exercise registration-commit and result-commit response loss, process exit, same-key retry after failed-write cleanup, concurrency and owner/version isolation in an isolated environment. Verify owner export omits keys/request hashes, normal scanning still runs once, and response loss plus browser refresh returns the same asset. Minimal command/attempt evidence is retained through account deletion; no file/form snapshot is stored in the browser or command row. The migration has only been applied to isolated test schemas; no running production/development service was upgraded. See [rules and validation](../docs/resource-marketplace-flows.md#632-上传命令幂等恢复与客户端重试0123).

## Paid checkout lookup handoff cutover (0129)

Drain old payment lookup/check executions, then deploy migration 0129, API and worker together. The index change allows multiple immutable lookup receipts to refer to the same active checkout check; it does not relax lookup job identity or active-check uniqueness. New workers revalidate and consume saved authenticated paid observations locally and schedule normal fulfillment, preserving both an older remote query and a later lookup when they overlap. A current provider outage is not a reason to discard already-saved payment proof. Unverified remote reads still require original merchant authentication.

Back up lookup records, their original request identity and job associations together. Down rejects shared check associations; do not delete evidence to make an old unique index fit. Verify original/replacement-job recovery, late expired responses during a paid lookup, exact transaction bindings, idempotent fulfillment and buyer-only export lineage before enabling payment traffic. Previously cancelled orders or removed files require separate original-payment/refund/delivery review; this migration does not rewrite their states or synthesize financial outcomes. Only isolated test schemas were upgraded; real-provider and production acceptance remain outstanding. See [rules](../docs/resource-marketplace-flows.md#520-已付款定位证据的履约交接0129) and [verification](../docs/resource-marketplace-flows.md#11123-已付款会话定位的证据复用与共享核对).

## Refund read execution recovery cutover (0128)

Stop/drain old financial readers and cleanup workers, then coordinate migration 0128, API, worker, frontend and `metrics-alert-check.sh`. The new history, exports, shared review and due-query rules depend on the execution and recovery tables. Database-only deployment is insufficient: old workers never register their reads and can still discard missing responses. Preserve execution registrations, their original check/job attempts and recovery associations in backups. Any execution row blocks destructive downgrade, including already captured or recovered rows.

The registry fences actual refund-list dispatch with a persisted absolute 20-second deadline. Recording the response and its original/late evidence is atomic. A subsequent complete original-merchant query can reconcile an unrecorded read only if it began after the prior read deadline plus five seconds and that old execution no longer holds a valid lease. It records a new immutable relationship, not a fabricated original response or financial operation. Partial/early/foreign reads cannot recover it; saved positive refund observations continue to require their own matching proof. The existing automatic scanner includes eligible missing reads with no local refund operation, retaining its backoff, batch bounds and failed-job policy.

Monitor `refund_read_unrecorded` separately from unmatched observed refunds; it counts affected payments only after the original read/persistence windows. The checker requires both live and test series and rejects old scrapes. Verify registration failure stops dispatch, process loss followed by replacement preserves restrictions, valid later recovery allows eligible cleanup, and late positive evidence remains protected. Owner export adds safe nested execution summaries; finance GETs retain permission/no-store requirements. Old unregistered reads, prolonged provider outages, disputed funds, real-provider semantics and production capacity still require acceptance. Only isolated schemas were migrated; see [rules and validation](../docs/resource-marketplace-flows.md#712-退款查询执行登记与中断恢复0128).

## Late refund read receipts cutover (0127)

Ship migration 0127, API, worker and frontend together after stopping financial traffic and draining old workers. The new query-detail, finance receipt history and account export paths depend on `product_refund_read_receipts`. Old workers can still discard late observations; updating only the database is insufficient. Preserve receipts together with original checks and `job_attempts` execution evidence when backing up or restoring.

The migration adds immutable receipts and extends the existing observation-gap view. It does not invent past responses, alter original query results or create refunds. Unmatched late observations participate in existing financial gates, the observation-review metric and 0126 delivery/account retention. A later complete authenticated query with applied matching evidence can release the gap. Empty queries cannot. Validate the complete and partial late-read cases, original-operation recovery, fixed-one finance pagination, owner export and file retention before cutover. GET history is not a provider query.

Down is permitted only with an empty receipt table. Any receipt, including resolved evidence, blocks destructive downgrade; never erase it to force rollback. Only isolated test schemas were migrated in this checkpoint. Real merchants, production query cost/capacity, Waffo active queries, manual/partial-refund disposition and seller settlement remain unaccepted. See [business rules](../docs/resource-marketplace-flows.md#711-被接手查询的迟到回执0127) and [validation](../docs/resource-marketplace-flows.md#11118-迟到退款观察的独立回执与全链路验证).

The matching worker also retains the current verified refund response while retrying PostgreSQL serialization/deadlock aborts within a five-second persistence budget. Retries reopen only the database evidence transaction, not the provider query or a financial operation. Constraint errors and uncertain connection/commit outcomes are not retried by this helper. At the 0127 checkpoint, persistent write failure and process loss before commit remain recovery gaps, not proof that no remote refund exists; 0128 above adds pre-dispatch registration and subsequent complete-query recovery for newly registered reads. No migration beyond 0127 is added for this change; deploy the API/worker together. See [the reproduced race and validation](../docs/resource-marketplace-flows.md#11119-退款证据写入冲突的原响应保留).

## Refund observation evidence cutover (0125)

Ship migration 0125, API, worker and the metrics checker together after stopping old financial traffic and draining old workers. New code queries the added views; deploying it before the migration fails rather than silently bypassing review. The migration adds derived evidence-gap views and extends the shared refund and delivery-retention policies. It does not alter observations, create operations or call providers. Existing saved observations immediately participate, so assess query cost against realistic refund history before rollout.

Verify failed and empty subsequent reads cannot release a prior unmatched refund, while an authenticated match to the original operation plus applied success can. Check buyer commands, worker redispatch, order capability projection, account/copy cleanup and `refund_observation_unresolved` in the appropriate mode. The checker expects both new fixed series and rejects an older API's scrape. Rollback refuses while unresolved observations exist; do not delete evidence to force it. The running environment has not been migrated. Unknown/manual/partial-refund disposition, Waffo query recovery, real-merchant and capacity acceptance remain incomplete; see [rules and verification](../docs/resource-marketplace-flows.md#79-历史退款查询证据不能被后续失败或遗漏覆盖0125).

The finance refund drawer now reads paginated historical query summaries and opens saved evidence on demand through GET `/admin/payments/{paymentID}/refund-checks` and `/{checkID}` (under `/api/v1`). Deploy the matching API and frontend together after 0125; no further migration is required. Both reads require `admin:finance`, disable caching, and make no provider calls. Verify older unmatched observations remain reachable after later failed queries, and distinguish historical unresolved counts from current review flags. This provides evidence access, not manual financial disposition; see [scope](../docs/resource-marketplace-flows.md#710-历史退款核对目录与单次证据读取) and [isolated verification](../docs/resource-marketplace-flows.md#11100-退款历史查询目录与证据查看验证).

### Unsettled funds and media cleanup (0126)

The delivery evidence inventory API also reads this view for `scope=unsettled` and `hasUnsettledFunds`; deploy its matching frontend to expose the filter and retention notice. It lists only delivery evidence gaps, including deleted buyers with unresolved funds, not every unresolved payment. Media permission does not grant access to private financial details or authorize resolution. See [inventory scope](../docs/resource-marketplace-flows.md#614-历史交付证据核对入口0111).

Stop/drain older API and cleanup workers before deploying migration 0126 with the matching API/worker. The new `product_order_funds_retention` view feeds ordinary independent-copy cleanup and account-original retention: pending checkout/refund, failed compensation, and any shared financial review retain required bytes after buyer access is revoked. An old account-cleanup binary still contains narrower SQL; a database-only rollout is insufficient. Legal holds and active purchase rights remain independent guards.

Verify both account deletion and previously queued ordinary cleanup retain a pending refund's copy, failed compensation retains evidence without an entitlement, and an unresolved second refund still protects a payment projected as refunded. Verified original success or ordinary failure may release the extra retention, subject to remaining rights/holds/review; compensation failure does not. Historical contracted originals without snapshots must also survive both accounts being deleted while funds remain unresolved. Rollback refuses with pending/failed refunds or financial review; never erase evidence to force it. This does not restore previously deleted files or implement final financial disposition. The running database is unchanged; see [scope](../docs/resource-marketplace-flows.md#634-退款未决时各清理入口统一保留交付0126).

## Missing checkout check reconciliation (0124)

Coordinate migration 0124, API, worker and the alert checker. The migration creates an immutable dispatch table, a shared eligibility view, a historical checkout-job index, and the new maintenance kind. It does not create provider calls or rewrite historical money/order states. The index is not concurrent: assess table size, write-lock impact and migration time in staging before rollout. Running environments have not been migrated.

A worker with payments enabled and a Stripe CheckoutReader scans on startup and each minute (100 candidates / 10 seconds). Only due, identity-backed open product sessions with consistent pending orders and no historical check job qualify. Queued/running/failed/cancelled/succeeded check history or unresolved event evidence prevents automatic replacement. The scheduler locks payment and order without waiting, rechecks eligibility, and commits a query job, dispatch, version/event and audit together. Existing handlers verify the original merchant and result before fulfilling or closing.

Verify an isolated missing-job fixture through both paid fulfillment and verified-unpaid expiry, concurrent scanners, locks, audit rollback, restart, owner export, `product_checkout_reconciliation` heartbeat and `checkout_check_missing` alert. Existing failed jobs continue through operator recovery. Any dispatch evidence blocks downgrade; recover forward. Empty-table rollback only removes its schema/index and heartbeat telemetry. See [rules and verification](../docs/resource-marketplace-flows.md#822-缺失支付会话核对任务的补建0124).

## Recovered paid checkout handoff cutover (0130)

Migration 0130 unifies authenticated terminal checkout evidence from session lookup and merchant identity recovery for scheduling, finance eligibility and missing-evidence metrics. Workers revalidate complete saved paid Checkout observations and current bindings before normal fulfillment, including original/replacement jobs and missing-check recovery without an available provider runtime. Conflicting sources remain unreconciled; unpaid recovery still requires an authenticated remote read. Owner exports preserve safe recovery/check lineage. This never manufactures the original checkout request or replaces subsequent historical refund checks. Coordinate migration/API/worker/frontend after draining old payment executions. Down restores the previous scheduling view and removes the shared projection without deleting receipts; stop programs that depend on the new view before rollback. Only isolated schemas were migrated. PaymentIntent-only recovery, previously cancelled orders, unsaved responses and production acceptance remain outside this checkpoint.

Before enabling traffic, verify future-expiry recovery, finance role/version/active-job guards, unavailable-runtime missing-check dispatch, immutable receipt agreement, one-time fulfillment and the follow-up refund check. A terminal evidence projection only qualifies scheduling; it does not authorize funds or access. The operator action is now “Check checkout payment”, since saved payment proof can qualify before expiry. See [rules](../docs/resource-marketplace-flows.md#521-商户恢复收款证据的履约交接0130) and [verification](../docs/resource-marketplace-flows.md#11124-商户恢复收款证据与运营核对闭环).

## Conflicting checkout evidence cutover (0131)

Migration 0131 preserves disagreements between authenticated paid Checkout queries, session lookups and merchant recovery receipts. Queries persist their original result before returning reconciliation failure; subsequent checks and queued fulfillment cannot ignore the conflict. Consistent additional sources are merged under the payment lock instead of stalling an existing check. Shared finance attention, ordinary refund restrictions and delivery retention include unresolved conflicts, while normal finance recovery and missing-check scheduling exclude them. The bounded `checkout_evidence_conflict` problem metric reports them independently of job status; ship the matching alert checker. Drain old payment executions and coordinate migration/API/worker/checker. Downgrade refuses to remove protection while conflicts exist; a conflict-free rollback preserves all receipts. Only isolated schemas were migrated. This is not complete financial adjudication, historical-order repair or production acceptance.

Verify consistent source additions, conflicting concurrent queries, late conflict before/after fulfillment, ordinary refund restrictions, real copy retention after entitlement revocation, finance capability/POST parity and live/test alert policy. Update the metric producer and checker together: the new checker treats a missing series as an invalid deployment, rather than assuming no conflict. A stopped ordinary check is not evidence that the financial discrepancy was resolved. See [rules](../docs/resource-marketplace-flows.md#522-并发付款证据的一致性与持久冲突保护0131) and [verification](../docs/resource-marketplace-flows.md#11125-付款查询并发交接与共享冲突保护).

## Closed historical checkout recovery (0132)

Migration 0132 preserves complete saved paid Checkout evidence for historical cancelled/payment-failed Stripe product orders. It blocks another checkout and retains delivery evidence, binds the original paid event/job without reopening the order, and schedules an authenticated refund-history preflight before creating any compensation obligation. Only a complete read requested and started after the recovery marker, with no unresolved funds, prior local refund obligations or historical entitlements, may queue the original event for the existing compensation/refund workflow. Unknown/manual/partial evidence remains held; finance can retry history reads with existing role/version/active-job guards. Missing checkout scheduling and its deferred binding trigger accept qualified closed orders without resetting failed jobs. Owner export includes safe recovery lineage; seller export excludes it. The `closed_checkout_paid` live/test problem metric tracks unfinished closed recovery, then existing refund metrics track compensation. Shared retention reads recovery progress; full funds eligibility stays at the processing boundary to avoid repeatedly expanding the complete refund policy in metrics. Drain old payment/cleanup executions and coordinate migration/API/worker/checker. Down refuses any saved recovery marker or unresolved closed paid state; it never deletes financial evidence. Only isolated schemas were migrated, with no live-provider calls or service restart. This does not complete historical refund adjudication, settlement, historical delivery migration or production acceptance.

Verify cancelled and payment-failed orders, concurrent/replayed recovery, unavailable providers, fresh reads after older history checks, unknown refunds surviving empty follow-up reads, automatic missing-check scheduling, finance capability/POST parity, owner export isolation, and actual retained bytes followed by cleanup after verified refund. Keep the metrics endpoint two-second budget; deploy the matching producer/checker together because a missing problem series is a deployment error. See [rules](../docs/resource-marketplace-flows.md#523-已关闭历史订单的收款恢复与退款前核对0132) and [verification](../docs/resource-marketplace-flows.md#11126-关闭历史订单的收款恢复与退款前核对).

## Existing historical refund confirmation (0133)

Apply 0133 with the matching API and worker after draining old payment and cleanup executions. It extends 0132 recovery only when a registered, complete post-marker refund read proves a unique original full successful refund and reconciles all known operations. Unknown/manual/partial/multiple-success refunds, active refund dispatch, identity conflicts and unexplained refund IDs remain held. The immutable confirmation links the original payment, check, event and operation; shared completion records refunded state, revokes existing rights and schedules cleanup without sending another refund command. Original paid-event completion re-enqueues cleanup if an earlier cleanup correctly retained the copy.

Verify signed callbacks before history reads, interrupted processing after a saved authenticated result, active old dispatch followed by a new complete read, finance retry guards, revoked download access, buyer-only export and actual copy cleanup. The existing `closed_checkout_paid` series remains active until recovery finishes; no metric kind or alert-checker change is introduced by 0133. Down refuses any confirmation record. Only an empty confirmation table can be rolled back to 0132, after stopping dependent programs; never remove evidence to enable downgrade. See [rules](../docs/resource-marketplace-flows.md#524-已关闭历史订单的原退款确认0133) and [verification](../docs/resource-marketplace-flows.md#11127-历史关闭订单的已发生退款确认). Running databases and services were not upgraded; isolated tests do not establish full financial adjudication or production acceptance.

## Automatic historical refund follow-up (0134)

Migration 0134 extends the existing refund reconciliation candidate view to cancelled/payment-failed Stripe product payments with saved closed-checkout recovery and matching order ownership/product. Pending/review-required original refund operations or registered read gaps qualify under the same merchant checks, completed-query/succeeded-job requirement, active-dispatch exclusion and backoff as ordinary refunds. The first automatic recovery check already counts toward backoff, so subsequent reads wait 30 minutes, 60 minutes and then progressively up to a day. The scheduler only enqueues reads; 0133 still requires full original refund proof before final state changes.

Coordinate migration/API/worker and drain incompatible older executions. Validate concurrent scheduling, restart, terminal original refunds, historical observation preservation, unresolved file retention, successful cleanup and finance recovery of failed reads. The existing `refund_check_due`, `refund_unresolved` and `closed_checkout_paid` metrics cover this path; there is no new metric kind. Down refuses unresolved original refund/read-gap obligations on closed recovered payments; never remove evidence to bypass that guard. An allowed rollback restores the prior candidate view. Only isolated schemas and simulated providers were used; running databases/services were not upgraded and real-provider acceptance remains pending. See [rules](../docs/resource-marketplace-flows.md#713-历史关闭订单的未决退款自动跟进0134) and [verification](../docs/resource-marketplace-flows.md#11128-历史未决退款自动跟进与最终确认).
