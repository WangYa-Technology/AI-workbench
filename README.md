# HCAI CHAT

HCAI CHAT is an international-first AIGC creation network being rebuilt with Go, Vue 3, and PostgreSQL. The product connects discovery, creation, owned assets, publishing, community participation, and transparent digital commerce.

The target experience is `en-US` first. Core flows are also localized for `zh-CN`. The source project at `/Users/helong/Work/newchat` is migration input and must remain read-only.

## Current checkpoint

Three local vertical workflows are operational:

`Discover work -> Remix -> durable Chat/Image/Video/Music generation -> Asset -> Publish -> Community`

`Discover demand -> proposal or direct accept -> Create with task context -> versioned Asset delivery -> review or revision -> Local Test settlement or dispute`

`Browse product -> review versioned license -> Local Test purchase -> receive entitlement and Asset -> use in Create -> inspect provenance -> refund and revoke entitlement`

The four creation modes have deterministic Local Test runtimes plus default-off real Provider boundaries: OpenAI Chat/Image, BytePlus ModelArk Seedance Video, and MiniMax Music 3.0. They require explicit credentials, paid-call approval, typed output validation, and audited route activation; the default development environment never calls an external Provider. `GET /api/v1/creation/capabilities` gives the Vue studio a non-secret active-route capability projection so controls stay aligned when an Admin changes the route.

Chat, Image, Video, and Music use clearly labeled deterministic local providers and produce text, JPEG, MP4, and WAV Assets. A default-off OpenAI adapter implements Chat through the Responses API and Image through the Image Generation API, but it is not registered without explicit configuration, credentials, and paid-call approval. The owner-scoped Generation Center provides URL-backed mode/status/UTC-date filters, stable cursor pagination, deep-linked evidence, and server-derived cancel/retry/download/reuse actions. Owned Assets, reference-only Community saves, the public Community feed, cross-version Asset-family usage, Community comments, publishing drafts, Marketplace orders, Account sessions, and notification-delivery evidence now have independent bounded stable pagination with duplicate-free continuation. Generation reservations/charges, task rewards, and Admin adjustments use explicitly labeled Local Test USD with auditable internal ledgers; the payment boundary additionally supports default-off Stripe Hosted Checkout, signed Webhooks, idempotent event processing, asynchronous refund/transfer recovery, creator payout-destination evidence, and permission-scoped Admin operations. Email identity, sessions, roles, permissions, profile settings, security logout, notifications, cross-domain public search, creator profiles, persisted publishing drafts, Asset version families and downstream usage evidence, Community interaction/governance, private support/copyright intake, immutable cross-domain risk rules and privacy-minimized registration account-link review, immutable model routing, versioned platform availability gates, tamper-evident audit chaining, operational diagnostics, default-off Developer Access, signed Webhooks, deep links, local Asset upload/scanning, and the local data export/deletion lifecycle are active. Production integrations and acceptance boundaries remain in progress; see `docs/MIGRATION_MATRIX.md`.

The current checkpoints include default-off staging acceptance commands for all four creation modes. OpenAI Chat/Image use a two-call check; BytePlus Video and MiniMax Music use a separate one-call-per-provider check. Each requires staging configuration, an exact confirmation phrase, and an explicit call ceiling. The repository never runs these paid checks automatically.

Task and product lists/details are public browsing surfaces. Authentication is required only for personal inventories and mutations such as publishing, proposing, accepting, purchasing, delivering, reviewing, or refunding; sign-in and registration return safely to the selected public item.

## Architecture

- `cmd/api`: Go REST API and embedded OpenAPI document.
- `cmd/worker`: independent worker that claims durable PostgreSQL job leases.
- `cmd/migrate`: idempotent embedded SQL migration runner.
- `cmd/seed`: opt-in deterministic demo users and workflow records for browser tests or intentional demo mode.
- `internal`: modular domain and platform packages.
- `internal/tasks`: demand briefs, proposals, assignment, commissioner cancellation, versioned delivery, review, dispute, history, and Local Test settlement.
- `internal/marketplace`: product discovery, licenses, immutable order evidence, Local Test purchase/refund, entitlements, and balanced ledger operations.
- `internal/discovery`: published work feeds/details, permission-aware cross-domain search, live PostgreSQL pattern indexes, evaluated/staged explainable ranking, and public creator projections.
- `internal/identity`: bcrypt email credentials, hashed sessions, profiles, roles, persisted permissions, OAuth provider boundaries, and account audit evidence.
- `internal/emailactions`: encrypted one-time verification/reset actions, durable delivery and expiry jobs, local mailbox delivery, and controlled dead-letter recovery.
- `internal/notifications`: user-scoped inbox, durable delivery worker, owner-visible attempt evidence, read state, optimistic preferences, allowlisted deep links, and idempotent transactional producers.
- `internal/creation`: durable four-mode generation commands, an exact-capability Provider runtime catalog, route-bound timeout/retry policy, typed output validation, local Provider outputs, cancellation/retry, owner-scoped filtered cursor history, server-derived actions, provenance, and billing integration.
- `internal/billing`: Local Test balances, reservations, immutable entries, transfers, statements, and controlled adjustments.
- `internal/admin`: permission-gated operational overview, user/content/generation/task/Provider/finance/risk/ranking controls, immutable risk-rule/model-route/platform-setting revisions, and audit evidence.
- `internal/observability`: bounded persistent request observations used by permission-scoped operational diagnostics.
- `internal/developer`: default-off personal Service Accounts, hash-only API keys, closed scopes, CIDR/expiry enforcement, explicit Developer API authentication, and controlled emergency revocation.
- `internal/webhooks`: AES-256-GCM signing-secret revisions, closed event subscriptions, PostgreSQL outbox delivery, HMAC-SHA256 requests, bounded retry/dead-letter evidence, and controlled replay.
- `internal/systemsettings`: transaction-local availability gates for registration, generation, publishing, checkout, and task creation.
- `internal/risk`: idempotent transaction-local signals from tasks, transactions, Community reports, media rejection, and privacy-minimized registration account links, with exact active-rule evidence.
- `internal/assets`: owned Asset metadata/provenance, immutable version families, owner-scoped downstream generation/Work/Product/Delivery usage evidence, reference-only saved Work projections, multipart upload, durable scanning, clean-only content access, and controlled Admin media review.
- `internal/platform/media`: local and S3-compatible immutable object storage, full/Range reads, authenticated SHA-256-bound HTTP scanning, deletion, and an explicitly authorized staging acceptance lifecycle.
- `internal/community`: persisted content drafts, atomic publication, Community interactions, reports, appeals, and governance evidence.
- `internal/datarights`: recent-auth export/deletion requests, durable artifact/retention/deletion jobs, Support-aware minimization, immutable receipts, and controlled legal holds.
- `internal/support`: requester-owned support and copyright cases, optimistic versions, append-only messages/events, resource visibility checks, notifications, and controlled Admin decisions.
- `web`: Vue 3, TypeScript, Vite, Vue Router, Pinia, and vue-i18n application.
- PostgreSQL is the source of truth for business state and async jobs.
- Generated media is written through the local media boundary under `MEDIA_ROOT` during development and through private S3-compatible storage in production.
- Uploaded JPEG, PNG, MP4, WAV, and plain-text files are limited to 10 MiB, detected from their bytes, written through the configured media backend, and unavailable until a durable scan job marks them clean.

Architecture decisions are recorded under `docs/adr`. The active requirement-by-requirement completion review is tracked in `docs/COMPLETION_AUDIT.md`.

## Requirements

- Go 1.26 or newer
- Node.js 24 or newer
- Docker with Docker Compose
- PostgreSQL client tools are optional but useful for diagnostics

## Local development

Install dependencies once:

```bash
make bootstrap
```

Start PostgreSQL, apply migrations, and run the API, worker, and Vue development server without inserting demo records:

```bash
make dev
```

Open [http://127.0.0.1:5173](http://127.0.0.1:5173). The API listens on `http://127.0.0.1:8080`; Vite proxies `/api`, `/health`, and `/ready`.

## Production runtime baseline

The repository includes non-root API, Worker, migration, and static Web container definitions under `deploy/`. They use an externally supplied PostgreSQL connection and an explicit production environment file; they do not include a production database, credentials, real Provider enablement, or a public deployment. Build all runtime images locally with:

```bash
make container-build
```

Use [deploy/README.md](deploy/README.md) for the required `.env.production` shape, proxy trust boundary, and release prerequisites. Production configuration requires the bundled private S3-compatible Asset adapter and authenticated HTTP scanner boundary; approved services, credentials, lifecycle policy, and staging acceptance remain deployment responsibilities.

After the deployment secret manager has injected the production environment, validate it without connecting to PostgreSQL or any Provider:

```bash
make production-config-check
```

The command fails unless `APP_ENV=production` and every runtime configuration invariant passes. Its JSON output contains only safe mode flags and counts; it never prints database locations, encryption keys, or Provider credentials.

After an isolated staging bucket/prefix and scanner have been approved, inject the same media configuration and run the one-shot external acceptance check:

```bash
MEDIA_ACCEPTANCE_CONFIRM=I_APPROVE_MEDIA_ACCEPTANCE_CALLS make media-staging-check
```

Without that exact confirmation the command exits before loading configuration or making a request. It also refuses local storage or the deterministic scanner. An approved run creates one random small text object, proves create-only writes, stat, full and Range reads, an authenticated content-bound clean scan, deletion, and post-delete absence. Failure paths attempt deletion with a cancellation-independent cleanup context. The JSON result contains adapter names, byte counts, scanner decision metadata, and a hash of the temporary key; it omits bucket, endpoint, object key, tokens, and credentials. This check does not replace private-bucket policy, encryption/lifecycle, backup, outage recovery, or cross-account application tests.

After the adapter check passes, use a separately approved disposable staging database to verify the same S3/Scanner pair through the application boundary:

```bash
MEDIA_APPLICATION_DATABASE_URL='postgres://staging-acceptance-database' \
MEDIA_APPLICATION_ACCEPTANCE_CONFIRM=I_APPROVE_MEDIA_APPLICATION_ACCEPTANCE_CALLS \
make media-application-staging-check
```

The wrapper creates a random `hcai_media_acceptance_*` PostgreSQL schema, injects it through `search_path`, and always drops it on exit. The command refuses any other schema, non-staging mode, local media adapters, or enabled local AI Provider mode. It registers two disposable accounts through the real HTTP contract, uploads one fixed non-user text Asset to S3, proves pending-content isolation, executes and completes the durable scan Job, verifies clean state, byte-identical full delivery, `206` Range delivery, cross-account denial, exactly-once Job/audit/notification evidence, and object deletion/post-delete absence. Safe JSON contains only adapter names, byte counts, states, and verification booleans; it omits database/bucket locations, object keys, account identifiers, credentials, tokens, and response bodies. This command makes real storage/scanner requests and is never run automatically.

After an approved OpenAI staging project, budget, credentials, region, and retention policy are available, inject the normal OpenAI configuration and run the bounded Provider smoke check:

```bash
APP_ENV=staging \
PROVIDER_ACCEPTANCE_CONFIRM=I_APPROVE_OPENAI_STAGING_CALLS \
OPENAI_ACCEPTANCE_MAX_CALLS=2 \
make provider-staging-check
```

This command makes exactly two real paid calls: one Responses API Chat request and one Image Generation request. It refuses development or production mode, a non-official base URL, local Provider registration, disabled paid-call approval, a missing capability, and an `auto` image size. It uses fixed repository-owned prompts with no user data and prints only bounded model, media type, output byte/dimension, and usage-counter evidence. It never prints or persists the prompts, Chat output, Image bytes, credentials, organization/project identifiers, or upstream response bodies. The check has a three-minute overall deadline and is never invoked by `make test`, `make build`, CI, application startup, or the repository's loopback drills.

After separate rights, budget, credentials, and retention approval for the Video and Music Providers, run their independent one-call staging check:

```bash
APP_ENV=staging \
CREATIVE_PROVIDER_ACCEPTANCE_CONFIRM=I_APPROVE_CREATIVE_STAGING_CALLS \
VIDEO_ACCEPTANCE_MAX_CALLS=1 \
MUSIC_ACCEPTANCE_MAX_CALLS=1 \
make creative-provider-staging-check
```

This command requires the exact official BytePlus and MiniMax HTTPS endpoints, both paid-call approvals, both enabled runtimes, a disabled local Provider, and non-empty staging credentials. It performs exactly one five-second Video call and one Music call, then prints only Provider/model, call count, MIME type, byte count, and Video dimensions. Prompts, media bytes, output URLs, credentials, request IDs, and upstream bodies are never printed or persisted. The six-minute deadline and exact call ceilings prevent an accidental retry loop; no external call is made unless every gate is explicitly satisfied.

Database backup and restore commands are documented in [deploy/BACKUP_RESTORE.md](deploy/BACKUP_RESTORE.md). They require explicit environment-provided database URLs, write owner-only checksummed custom-format archives, and never restore implicitly to the application database.

The internal metrics endpoint and executable alert thresholds are documented in [deploy/OBSERVABILITY.md](deploy/OBSERVABILITY.md). `/metrics` is intentionally not exposed through the public Web proxy; connect it to a private scraper or authenticated operations proxy before production use.

Normal development starts with migrations only, never inserts demo content, and hides demo-account shortcuts. To intentionally run the seeded showcase instead:

```bash
make dev-demo
```

Individual processes are available through `make db-up`, `make migrate`, `make seed`, `make api`, `make worker`, and `make web`.

Run the isolated real-process worker recovery drill with PostgreSQL client tools and `jq` available:

```bash
make recovery-drill
```

The drill uses a temporary schema, media directory, and loopback port (`DRILL_HTTP_PORT`, default `18081`). It stops only the worker process it creates, submits a generation while that worker is offline, restarts processing, verifies one Asset and one Local Test charge plus idempotent replay and Admin diagnostics, then removes all temporary state.

Run the isolated Stripe payment-provider drill with PostgreSQL client tools, `jq`, `curl`, `lsof`, and `openssl` available:

```bash
make payment-drill
```

This drill builds real API and worker binaries plus a loopback-only Stripe fixture, applies migrations and demo seed data in a temporary PostgreSQL schema, and enables Stripe test mode only inside the child processes. It proves one hosted product Checkout call, signed payment and refund Webhooks, durable worker processing, entitlement grant then revocation, Checkout/Webhook/refund idempotency, one Connect Express account, two single-use Account Link renewals, signed `account.updated` synchronization to a verified payout destination, and Admin payment evidence. `PAYMENT_DRILL_HTTP_PORT` and `PAYMENT_DRILL_FIXTURE_PORT` default to `18082` and `18083`; the script rejects occupied ports and removes every process, file, and schema it creates.

Run the isolated wallet top-up and subscription checkout drill with the same PostgreSQL client tools, `jq`, `curl`, `lsof`, and `openssl`:

```bash
make billing-drill
```

This drill uses real API and Worker binaries plus the loopback Stripe fixture to create a hosted USD wallet top-up and a paid subscription checkout. It submits signed payment Webhooks, waits for Worker fulfillment, verifies the wallet ledger and subscription points, and replays both Checkout and Webhook requests to prove that each balance or entitlement is granted exactly once. `BILLING_DRILL_HTTP_PORT` and `BILLING_DRILL_FIXTURE_PORT` default to `18086` and `18087`; all processes, media, schema, and ports are cleaned up afterward.

## Configuration

Copy values from `.env.example` into the process environment as needed. Important variables:

| Variable | Purpose |
| --- | --- |
| `APP_ENV` | Runtime environment. Production enables stricter validation. |
| `DATABASE_URL` | PostgreSQL connection string. |
| `HTTP_ADDR` | Go API listen address. |
| `WEB_ORIGIN` | Single allowed browser origin for local CORS. |
| `TRUSTED_PROXY_CIDRS` | Comma-separated non-zero CIDR prefixes for deployment proxies whose sanitized `X-Forwarded-For` value may be used for privacy-minimized network evidence. Empty means forwarded headers are ignored. |
| `MEDIA_ROOT` | Local generated-media directory. |
| `MEDIA_STORAGE_ADAPTER` | `local_file` for development or `s3` for production. Production fails closed unless `s3` is selected. |
| `MEDIA_S3_BUCKET` / `MEDIA_S3_REGION` | Private object bucket and signing region used by the S3-compatible adapter. |
| `MEDIA_S3_ENDPOINT` | Optional HTTPS S3-compatible origin; leave empty for the AWS default endpoint. Development HTTP is loopback-only. |
| `MEDIA_S3_ACCESS_KEY_ID` / `MEDIA_S3_SECRET_ACCESS_KEY` / `MEDIA_S3_SESSION_TOKEN` | Runtime-only object-storage credentials. They are never persisted in Asset, audit, or API data. |
| `MEDIA_S3_PATH_STYLE` / `MEDIA_S3_PREFIX` | S3 addressing mode and bounded private object-key prefix. |
| `MEDIA_SCANNER_ADAPTER` | `local_deterministic` for development or `http` for production. Production fails closed unless `http` is selected. |
| `MEDIA_SCANNER_URL` / `MEDIA_SCANNER_TOKEN` | HTTPS scanner endpoint and Bearer credential. The scanner receives bounded raw upload bytes plus object-key and SHA-256 headers. |
| `MEDIA_SCANNER_TIMEOUT_SECONDS` | Scanner request deadline, bounded to 1-120 seconds; defaults to 30. |
| `MEDIA_APPLICATION_DATABASE_URL` | One-shot application-media acceptance database. It must be an approved disposable staging database and must not include `search_path`; the wrapper creates and removes an isolated schema. |
| `LOCAL_PROVIDER_SOURCE` | Project-owned image copied by the deterministic image provider. |
| `LOCAL_PROVIDER_ENABLED` | Enables local test generation outside production. |
| `COOKIE_SECURE` | Requires secure session cookies in production. |
| `WEBHOOK_ENCRYPTION_KEY_B64` | Base64-encoded 32-byte AES key. Required in production; non-production has a deterministic local fallback. |
| `WEBHOOK_ALLOW_LOCAL` | Allows loopback HTTP receivers for local verification. Must be false in production. |
| `EMAIL_DELIVERY_MODE` | `local_file` writes development-only `.eml` files; production must use `disabled` until an approved adapter exists. |
| `EMAIL_ACTION_ENCRYPTION_KEY_B64` | Base64-encoded 32-byte AES key for one-time identity action tokens. Required in production; non-production has a deterministic local fallback. |
| `STRIPE_ENABLED` | Registers the Stripe payment runtime and hosted checkout/Webhook workflow. Defaults to `false`. |
| `STRIPE_LIVE_MODE` | Selects Stripe test or live mode. Live mode is rejected until separately approved. Defaults to `false`. |
| `STRIPE_LIVE_MODE_APPROVED` | Explicit operational approval required before live mode. Defaults to `false`. |
| `STRIPE_SECRET_KEY` | Stripe secret key; must match `sk_test_` or `sk_live_` mode and is never persisted in business data. |
| `STRIPE_WEBHOOK_SECRET` | Stripe endpoint signing secret (`whsec_...`) used to verify raw event bytes. |
| `STRIPE_BASE_URL` | Defaults to `https://api.stripe.com/v1`; development HTTP is loopback-only, production is pinned to the official endpoint. |
| `STRIPE_API_VERSION` | Pinned Stripe API version used by request and Webhook contracts. |
| `STRIPE_WEBHOOK_TOLERANCE_SECONDS` | Signed-event timestamp tolerance, bounded to 60-900 seconds; defaults to 300. |
| `PAYMENT_PROVIDER` | Product checkout provider: `stripe`, `waffo_pancake`, or reserved `epay`. Defaults to `stripe`. |
| `WAFFO_ENABLED` | Registers the server-side Waffo runtime and connector boundary. Defaults to `false`; requires the Waffo deployment values below. |
| `WAFFO_ENVIRONMENT` / `WAFFO_PRODUCTION_APPROVED` | Selects Waffo `test`/`prod`; production requires an explicit approval gate. |
| `WAFFO_MERCHANT_ID` | Waffo merchant identity bound to the rotated connector private key. It is deployment configuration and is never exposed to the browser. |
| `WAFFO_STORE_ID` | Optional deployment fallback store ID; Admin can provide the store ID in the persisted Provider configuration. |
| `WAFFO_CONNECTOR_URL` / `WAFFO_CONNECTOR_TOKEN` | Authenticated API-to-connector boundary. Production requires an HTTPS connector; the token is never persisted. |
| `WAFFO_PRODUCT_ID_ONETIME` / `WAFFO_PRODUCT_ID_SUBSCRIPTION` | Optional deployment fallback product IDs created and published in the matching Waffo environment. One-time checkout requires an effective one-time product ID from either deployment configuration or Admin. |
| `OPENAI_ENABLED` | Registers the OpenAI Chat/Image runtime only when explicit paid-call approval and credentials are also present. Defaults to `false`. |
| `OPENAI_PAID_CALLS_APPROVED` | Explicit operational authorization required before `OPENAI_ENABLED=true` is accepted. Defaults to `false`. |
| `OPENAI_API_KEY` | OpenAI API credential. Required only for enabled runtime processes and never persisted in business data. |
| `OPENAI_BASE_URL` | Defaults to `https://api.openai.com/v1`; production accepts only that exact official HTTPS endpoint, while development HTTP is loopback-only. |
| `OPENAI_CHAT_MODEL` / `OPENAI_IMAGE_MODEL` | Exact registered capabilities. Defaults to `gpt-5.6-terra` and `gpt-image-2`. |
| `OPENAI_CHAT_MAX_OUTPUT_TOKENS` | Bounded Responses API output budget; default `2048`, allowed range `1`–`32768`. |
| `OPENAI_IMAGE_SIZE` / `OPENAI_IMAGE_QUALITY` | Reviewed image output settings. Defaults to `1024x1024` and `medium`. |
| `OPENAI_ORGANIZATION` / `OPENAI_PROJECT` | Optional OpenAI routing headers. |
| `OPENAI_RECONCILIATION_ENABLED` | Registers only the separately gated organization Costs reader; defaults to `false` and does not enable generation traffic. |
| `OPENAI_RECONCILIATION_APPROVED` | Explicit approval required before aggregate Provider cost reconciliation can be enabled. |
| `OPENAI_ADMIN_API_KEY` | Separate organization-admin credential for the Costs endpoint; never reuse or persist the generation API key. |
| `VIDEO_ENABLED` / `VIDEO_PAID_CALLS_APPROVED` | Independent gates for the default-off BytePlus ModelArk Seedance Video runtime. |
| `VIDEO_API_KEY` / `VIDEO_BASE_URL` / `VIDEO_MODEL` | BytePlus credential, API base, and exact model capability. Production fixes the base to `https://ark.ap-southeast.bytepluses.com/api/v3`; development HTTP is loopback-only. |
| `VIDEO_POLL_INTERVAL_SECONDS` / `VIDEO_TIMEOUT_SECONDS` | Bounded asynchronous task polling and overall Provider timeout. |
| `MUSIC_ENABLED` / `MUSIC_PAID_CALLS_APPROVED` | Independent gates for the default-off MiniMax Music 3.0 runtime. |
| `MUSIC_API_KEY` / `MUSIC_BASE_URL` / `MUSIC_MODEL` | MiniMax credential, API base, and exact model capability. Production fixes the base to `https://api.minimaxi.com/v1`; development HTTP is loopback-only. |

Production startup fails when PostgreSQL does not use `sslmode=verify-full`, `WEB_ORIGIN` is not a public path-free HTTPS origin, placeholders remain, S3 storage or the authenticated HTTP scanner is not configured, the local provider is enabled, secure cookies are disabled, loopback Webhook targets are allowed, either 32-byte encryption key is absent or the two keys are reused, identity email delivery is not `disabled`, or the OpenAI endpoint is not the official HTTPS API. Stripe production enablement additionally requires approved live mode, matching live credentials, and the pinned official endpoint. OpenAI registration additionally fails without both explicit paid-call approval and a credential. Forwarded client headers are ignored unless the direct TCP peer is inside `TRUSTED_PROXY_CIDRS`; the deployment proxy must sanitize and replace them before forwarding. Network evidence is stored only as a one-way minimized hash, and retention, notice, and legal-basis acceptance remain production responsibilities. No secrets, paid-provider credentials, real payment details, or personal production data belong in the repository.

## Database and seed

Migrations are embedded from `internal/platform/database/migrations` and recorded in `schema_migrations`. Running `make migrate` more than once is safe.

`make seed` is deterministic and idempotent. It creates records under reserved UUIDs and labels all reference content as demo content. It does not fabricate audience size, transaction volume, ratings, or commercial relationships.

The current core migration has a corresponding down migration. Future irreversible migrations must document why rollback is unsafe and provide a forward recovery procedure.

## API contract

OpenAPI is the public contract:

- Source: `internal/transport/httpapi/openapi.yaml`
- Runtime: `http://127.0.0.1:8080/api/openapi.yaml`
- Generated Vue types: `web/src/api/schema.d.ts`

Regenerate frontend types after changing the contract:

```bash
npm --prefix web run generate:api
```

## Stripe payment boundary

Stripe is disabled by default. When `STRIPE_ENABLED=true` is explicitly configured with a matching test key and endpoint signing secret, product checkout, task funding, wallet top-ups, and subscription purchases create hosted Checkout Sessions in test mode. The API never accepts card numbers or stores payment credentials. Entitlements, task assignment, wallet credits, subscription points, transfers, and refunds advance only after a signed Stripe event is verified against the pinned API version and configured test/live mode.

`POST /api/v1/payments/webhooks/stripe` accepts only bounded JSON with a valid `Stripe-Signature`. Events are minimized before persistence, keyed by `(provider, provider_event_id)`, and queued for the worker. Replayed events are acknowledged idempotently; a changed payload for the same event ID is rejected. Worker jobs perform provider-side refunds and creator transfers with bounded retries, while Admin Finance exposes exact payment, destination, event, and recovery evidence with optimistic versions and audit requirements.

Local and staging verification must use Stripe test mode (`STRIPE_LIVE_MODE=false`, `sk_test_...`) against a loopback-compatible test server or Stripe's test API. Production requires `STRIPE_LIVE_MODE=true`, separate live-mode approval, `sk_live_...`, a public HTTPS origin, the official Stripe API endpoint, a registered Webhook endpoint, merchant/legal scope, and sandbox-to-production reconciliation evidence. No real payment activity is authorized by this repository or its default configuration.

`make payment-drill` is the reproducible pre-credential acceptance baseline. It never contacts Stripe and does not replace the required test-account acceptance for Connect onboarding, KYC, disputes, tax, invoices, reconciliation, or live-mode approval.

## Waffo Pancake payment boundary

Waffo Pancake is integrated through the private Node connector at
`services/waffo-connector`. The connector is the only process that imports
`@waffo/pancake-ts` and owns `WAFFO_PRIVATE_KEY` or
`WAFFO_PRIVATE_KEY_BASE64`; the Go API and database never receive the private
key. It exposes authenticated internal checkout, refund, and raw Webhook
verification routes, while the public API receives
`POST /api/v1/payments/webhooks/waffo` and persists minimized, idempotent event
evidence.

The Admin Finance page can maintain the provider's enabled state, environment,
store ID, and one-time/subscription product IDs. The same provider boundary
serves product checkout, wallet top-ups, and subscription purchases. The merchant ID is displayed
for audit and must match the deployment identity bound to the connector key.
Secret material and
the connector token remain deployment-managed. The runtime is registered only
when `WAFFO_ENABLED=true` passes the fail-closed configuration checks; the
checkout provider uses the single enabled row in the Admin configuration (and
falls back to `PAYMENT_PROVIDER` for an unconfigured installation). The
deployment environment must still select `PAYMENT_PROVIDER=waffo_pancake` or
register the Waffo runtime before it can process Waffo traffic. The
development script starts the connector automatically in that mode, using the
already injected environment, while stripping the private key from the API and
Worker child environments.

Create or select Waffo one-time and subscription products in the Dashboard,
publish them in the target environment, and register the public HTTPS Webhook
for `order.completed`, `subscription.activated`, `subscription.payment_succeeded`, `refund.succeeded`,
and `refund.failed` as described in
[`services/waffo-connector/README.md`](services/waffo-connector/README.md).
Because the private key supplied in a chat message is considered exposed, it
must be revoked and rotated before enabling the connector. A real checkout
also needs the resulting Waffo product ID; neither value is present in this
repository.

Run the isolated OpenAI Chat/Image runtime drill with PostgreSQL client tools, `jq`, `curl`, and `lsof` available:

```bash
make provider-drill
```

This drill enables the configured OpenAI profiles only inside an isolated test schema, activates immutable Chat and Image routes through the Admin API, and runs real API/Worker binaries against a loopback OpenAI fixture. It verifies typed text and PNG outputs, Asset persistence, Local Test reservation/capture, generation idempotency, and exactly one fixture call per mode. `PROVIDER_DRILL_HTTP_PORT` and `PROVIDER_DRILL_FIXTURE_PORT` default to `18084` and `18085`; all child processes, media, schema, and ports are cleaned up afterward. It never contacts OpenAI or incurs a paid call.

## Billing statements

`GET /api/v1/billing/statement` returns the authenticated owner's immutable entries in stable `created_at DESC, id DESC` order. The endpoint supports `direction`, `entryType`, `dateFrom`, `dateTo`, `limit`, and opaque `cursor` parameters, with a maximum page size of 50 and `nextCursor` for continuation. The Credits & billing workspace keeps these filters in the URL and appends later pages without duplicates. All balances and entries are explicitly Local Test evidence; no real charge or payout occurs.

Admin Support and Risk inventories are bounded and cursor-paginated. Support accepts `q`, `status`, `category`, `limit`, and `cursor`, preserving active-case priority before stable update order. Risk accepts `q`, `status`, `severity`, `limit`, and `cursor`, preserving operational status priority, score, and detection order. Risk deep links may also pair `resourceType` with `resourceId`; the server applies that exact focus before pagination, so newly reported evidence remains reachable under a large backlog. Both Admin views restore namespaced filters from the URL, append pages without duplicates, and reload the active query after controlled operations. Invalid filters, unpaired focus, or modified cursors fail closed with localized `422` responses.

The private owner Support index is independently cursor-paginated after requester isolation. It uses stable `updated_at DESC, id DESC` traversal with a maximum page size of 50; the Vue index initially loads 20 records and appends later pages without duplicates while keeping exact case deep links available.

The private Asset workspace independently paginates owned Assets, reference-only saved Works, and downstream family usage evidence. Asset pages apply ownership, latest-version collapse, and active purchase entitlement before stable `created_at DESC, id DESC` traversal. Saved Works apply bookmark ownership plus published/clean visibility before stable `saved_at DESC, post_id DESC` traversal. Asset detail embeds 20 recent uses and continues through `/assets/{assetId}/usages`, where ownership is verified before a stable mixed-kind cursor is decoded. Every page is capped at 50 and malformed cursors fail with localized `422` responses.

Community comments, private publishing drafts, Marketplace orders, and Account sessions also use bounded opaque-cursor pages instead of silent fixed windows. Comments preserve chronological order; drafts, orders, and sessions preserve stable newest-first operational order. The Vue surfaces append without duplicate IDs, and exact session revocation determines current-cookie clearing from the owned session update rather than a paginated inventory scan.

The public Community feed applies published Post/Work and clean-Asset visibility before stable reverse-chronological cursor pagination. Authenticated notification-delivery evidence applies account ownership before its cursor, includes durable delivered/suppressed attempt state, and never exposes job payloads or worker internals. Both pages default to 20, accept 1-50, reject modified cursors, and append without duplicate IDs.

The permission-scoped Admin user directory supports `q`, `role`, `status`, `limit`, and opaque `cursor` parameters. It uses stable `created_at DESC, id DESC` traversal with a maximum page size of 50; the Admin UI restores filters from its URL and appends later pages without duplicates. Controlled account changes retrieve the exact updated user after commit, independent of the current directory page.

The Admin content and uploaded-media inventories are also bounded and cursor-paginated. Content supports `q`, `type`, and `status` over stable `updated_at DESC, id DESC` order. Media supports `q`, `kind`, and `status`, preserving review-first operational priority before stable `created_at DESC, id DESC` order. Both cap pages at 50, restore filters from the Admin URL, append without duplicate IDs, and reload the active query after a controlled operation. Malformed filters or modified cursors fail closed with localized `422` responses.

Admin Community report and appeal inventories use independent URL-restorable filters and stable `created_at DESC, id DESC` cursor pagination instead of fixed 200-row windows. Reports support `q`, `type`, `category`, and `status`; appeals support `q`, `type`, and `status`. Both cap pages at 50, reject malformed filters or cursors with localized `422` responses, append without duplicate IDs, and reload the active queue after a controlled decision.

Immutable Admin System Settings and Risk Rules expose their exact active revision separately from a stably paginated descending version history. Both histories cap pages at 50, use opaque cursors, append without duplicate revision IDs, and reject malformed continuation input with localized `422` responses. Activating a new revision reloads the first page without weakening optimistic version, confirmation, reason, permission, or audit controls.

Admin Discovery ranking follows the same exact-state boundary for active and candidate policies while paginating immutable revisions independently. Offline ranking evaluations and index-analysis runs have separate stable cursors, limits, continuation controls, and immutable-ID de-duplication, so a large history in one stream cannot hide evidence in another.

Admin model routing resolves the exact active Chat, Image, Video, and Music revisions independently from its mode-scoped immutable history. Route history accepts `mode`, `limit`, and an opaque descending-version `cursor`, caps pages at 50, rejects cursors without their mode, and preserves independently loaded history when an operator switches creation modes. Subsequent generations continue to retain the exact route revision selected at submission time.

The authenticated Community report/appeal history also applies viewer visibility before stable cursor pagination, caps pages at 50, and appends without duplicate records. The Admin Webhook and identity-email recovery queues expose independent search, exact event/action filters, page bounds, and opaque cursors. Their URL-backed Vue filters survive reload; replay, retry, and cancellation reload the active query. Recovery projections remain deliberately minimized and never expose signing secrets, full endpoint URLs, response bodies, full recipient addresses, email bodies, or action tokens.

The Admin task operations inventory supports `q`, `status`, `disputeStatus`, `limit`, and opaque `cursor` parameters. It preserves unresolved-dispute priority before stable `updated_at DESC, id DESC` traversal, caps pages at 50, restores namespaced filters from the Admin URL, and reloads the active queue after a decision. Malformed filters and modified cursors fail closed with localized `422` responses.

Admin generation, Local Test finance, and immutable audit inventories are also bounded and cursor-paginated. Generations support `q`, `mode`, and `status` over stable `created_at DESC, id DESC` order. Finance supports `q` and `state` over stable `updated_at DESC, user_id DESC` order and reads an adjusted account exactly after commit. Audit supports `q`, exact `action`, and exact `resourceType`, traversing the tamper-evident chain by descending sequence. Each page is capped at 50, uses independent URL-restorable filters, appends without duplicates, and returns a localized `422` response for malformed filters or cursors.

## Developer Access

Developer Access is globally disabled by default. An administrator can enable bounded personal Service Accounts from the Admin Developer Access tab. Owners then issue API keys from Account settings; plaintext is displayed once, while PostgreSQL retains only a SHA-256 secret hash and safe public hint.

The initial closed scope is `developer:identity:read`. A key can call the explicit versioned principal contract:

```bash
curl -H "Authorization: Bearer $HCAI_DEVELOPER_KEY" \
  http://127.0.0.1:8080/api/v1/principal
```

Developer keys never authenticate existing product Cookie routes. Expiry, rotation, key or Service Account revocation, global disable, scope mismatch, and optional IPv4/IPv6 CIDR restrictions all fail closed. Administrator emergency revocation requires permission, reason, confirmation, exact version, and audit evidence.

### Signed Webhooks

Signed Webhooks share the default-off Developer Access control but use independent credentials and lifecycle state. Owners subscribe an endpoint to the closed event catalog from Account settings. The one-time `whsec_...` signing secret is encrypted at rest and signs `timestamp.rawBody` with HMAC-SHA256; it is never included in endpoint inventory, Admin dead-letter evidence, logs, exports, or response records.

Delivery uses the PostgreSQL outbox and worker. Transient failures retry at most five times; receiver configuration failures become dead letters. Admin replay requires permission, reason, confirmation, exact version, audit evidence, and original-delivery lineage. Production requires HTTPS public targets and an explicit encryption key. `WEBHOOK_ALLOW_LOCAL=true` exists only for a local loopback receiver such as the Playwright protocol test.

Account endpoint inventory returns five recent safe deliveries per endpoint and an opaque continuation cursor when older evidence exists. `GET /api/v1/account/developer-webhooks/{endpointId}/deliveries` is owner-scoped, caps pages at 50, and preserves stable `(created_at DESC, id DESC)` traversal without expanding the whole Account response.

## Identity email delivery

Email verification and password recovery use encrypted, expiring, one-time actions backed by PostgreSQL jobs. Password-reset requests return the same accepted response for known and unknown addresses. A successful reset changes the bcrypt credential and revokes every active session; consumed, expired, cancelled, or replaced actions erase token material.

For local development, `EMAIL_DELIVERY_MODE=local_file` writes private `.eml` files to `MEDIA_ROOT/mailbox/<user-id>/<action-id>.eml`. The mailbox is deterministic test infrastructure, not a production mail server. Files and directories use owner-only permissions, are removed with local account deletion, and must never be served as public media.

Production remains fail closed with `EMAIL_DELIVERY_MODE=disabled` until an approved delivery Provider, bounce/complaint processing, suppression policy, and operational acceptance are implemented. The Admin recovery queue exposes only masked recipient and bounded attempt evidence; retry or cancellation requires permission, reason, explicit confirmation, optimistic version agreement, notification, and audit evidence.

The owner Account history defaults to five recent actions and exposes an opaque stable cursor for older evidence. Pages are capped at 50, enforce account ownership before cursor conditions, and never return message bodies, action tokens, ciphertext, or complete recipient addresses.

Owner and Admin data-rights request histories are independently cursor-paginated with pages capped at 50. Admin legal holds use a separate active-priority cursor with a frozen evaluation timestamp, so a hold expiring during traversal does not duplicate or skip historical evidence. Account and Admin pages append each resource independently and reload synchronized first pages after a hold change.

## Testing

Run Go unit and PostgreSQL integration tests plus frontend unit tests:

```bash
make test
```

Run static checks and the production build:

```bash
make lint
make build
```

Run all browser workflows. Playwright starts an isolated stack on ports `15173` and `18080`, seeds a temporary PostgreSQL schema, and removes that schema and its generated media when the run ends:

```bash
make e2e
```

The browser suite includes a strict axe-core WCAG 2.2 AA gate for critical user and Admin routes in light and dark themes. It also verifies skip navigation, client-route focus, current-page semantics, keyboard scrolling for text Assets, and reduced-motion operation.

PostgreSQL integration tests use an isolated temporary schema and never truncate the development schema. Set `TEST_DATABASE_URL` to use a different integration database.

## Internationalization

English source messages live in `web/src/i18n/messages/en-US.ts`; Simplified Chinese messages live in `web/src/i18n/messages/zh-CN.ts`. UI text must go through vue-i18n. Currency, dates, numbers, plurals, and time zones use internationalization APIs rather than manual string concatenation.

The frontend unit suite enforces identical locale keys and interpolation parameters, rejects static user-facing copy in Vue templates, and checks that every stable Go API error code has localized client copy. API failures are rendered from their stable code rather than server prose, with a localized fail-closed fallback and request ID. Browser coverage verifies the Chinese core routes, Admin system-enum labels, overflow, raw keys, and a real authentication failure path.

The default currency is USD, and timestamps are stored in UTC. User-facing transaction records must show the applicable locale and IANA time zone.

## Provider safety

Every Chat, Image, Video, and Music integration remains behind `creation.ProviderRuntime`. Submission, retry, Admin enablement, and immutable model-route activation require an exact registered `(provider, mode, model)` capability. Durable jobs inherit the route revision's timeout and retry limit, while output bytes must pass the closed typed media contract before Asset creation. Local adapters are deterministic, mode-specific, and marked `local_test` in API and UI state. The bundled OpenAI Chat/Image, BytePlus Seedance Video, and MiniMax Music adapters are default-off and separately gated by process configuration, Provider Profile enablement, and audited model-route activation. Video output URLs are downloaded immediately because upstream retention is short-lived; Music output is bounded and signature-validated before Asset insertion.

Use `docs/PROVIDER_INTEGRATION.md` for the implementation and staging acceptance contract. Registering code alone never enables traffic: the runtime must be configured, its Provider profile must be explicitly enabled, and an audited route revision must be activated.

`make provider-staging-check` is a separately authorized two-call smoke check, not a runtime activation command. It does not enable Provider Profiles or model routes and does not replace application-level timeout, cancellation, retry, rate-limit, content-policy, cost-reconciliation, or Admin canary-disable acceptance.

Do not enable production providers, issue paid calls, deploy, change DNS or cloud resources, or create real payment activity without explicit approval and credentials. Missing production configuration must fail closed.

## Security and trust

- Session tokens are stored as hashes; browser sessions use HttpOnly, SameSite cookies.
- Passwords use bcrypt; the API never returns password hashes, session tokens, raw IP addresses, or OAuth secrets.
- Identity email tokens use 32-byte random values, SHA-256 lookup hashes, and AES-256-GCM ciphertext at rest. Raw tokens, ciphertext, message bodies, reset URLs, and complete recipient addresses are excluded from APIs, logs, audit metadata, Admin projections, notifications, and data exports.
- Verification and password-reset actions are one-time and expiring. Reissuing cancels prior active actions, password reset revokes all sessions, and lifecycle completion erases token material.
- Developer API keys are shown once and persisted only as SHA-256 secret hashes with safe public hints. Product routes remain Cookie-only; Bearer keys are limited to the explicit Developer API contract and fail closed when expired, revoked, disabled, out of CIDR, or missing the required scope.
- Developer Access is globally default-off. Owner rotation/revocation and administrator emergency revocation are versioned and audited; Admin inventory and audit metadata exclude plaintext keys, secret hashes, and raw network addresses.
- Webhook signing secrets are independent AES-256-GCM revisions returned once. Delivery signs exact raw bytes, rejects redirects and unsafe targets, stores no response body, and bounds retry/dead-letter recovery. Account deletion erases signing ciphertext and minimizes retained event evidence.
- Authentication failures never create an implicit demo session. Local demo accounts are explicit and unavailable in production.
- Google and GitHub remain fail closed until credentials and staging verification are complete.
- Notification producers atomically queue one owner-scoped row and one durable job. The worker applies the latest preference, records immutable delivered/suppressed evidence, and exposes only a safe owner projection without payload, lease, worker, or raw-error data.
- Deep links are internal and allowlisted, read state is independent from opening the linked workflow, and task, order, refund, generation, Community, governance, Asset, support, and data-rights producers deduplicate on replay.
- Community comments, reactions, bookmarks, and follows are persisted and scoped to the authenticated actor.
- Reports and appeals retain ordered governance evidence. Admin decisions require permission, reason, confirmation, notifications, and audit; upheld appeals restore the prior content state atomically.
- Uploaded Assets default to pending and fail closed until scanning completes. Review/rejected content cannot be read or published; Admin Media changes require permission, reason, confirmation, owner notification, and audit evidence.
- Asset versions store new bytes as ordered family members and pass through independent scans; prior versions and predecessor links remain inspectable, while purchased originals cannot be versioned.
- Publishing drafts are private PostgreSQL records with optimistic versions. Refresh and cross-session recovery are supported, and publish revalidates ownership, clean scan state, disclosure, and purchased-original restrictions.
- Data export requires a session issued within 15 minutes and exact handle confirmation. The private JSON package is byte-bounded, checksum-addressed, owner-only, and purged after seven days by a durable retention job.
- Account deletion has a 30-day cancellation window. Local primary processing revokes access, deletes owned local media, anonymizes identity/content, redacts Support free text, retains bounded transaction/audit/safety/Support facts, and creates an immutable per-domain receipt. Controlled legal holds block and resume deletion with hashed authority evidence.
- Production backup expiry and external Provider deletion are not claimed by local receipts; those steps remain fail-closed until verified production integrations and legal acceptance exist.
- Support cases are requester-owned and private. Copyright intake requires a stable public platform target and bounded claimant statement, rejects common high-risk identifiers, and does not claim to determine legal ownership.
- The global product shell exposes Terms, Privacy, Cookies, Acceptable Use, AI Disclosure, Licensing, Refunds, Copyright, and private Support to anonymous and authenticated users. It labels the bundled copy as Local Test product terms and keeps production legal acceptance explicit.
- Support messages/events are append-only during normal operation. The data-rights worker can write only fixed redaction markers while preserving every structural evidence field; Admin replies and state decisions still require `admin:support`, a specific reason, explicit confirmation, optimistic version agreement, requester notification, and audit evidence.
- Asset version events are append-only during normal operation. Owner exports include the event history; account deletion clears version notes and permits only the fixed event-reason marker through the constrained maintenance boundary.
- Task disputes, Local Test refunds, Community reports, uploaded-media rejection, and registration account-link thresholds atomically create idempotent risk signals. Account-link evidence retains aggregate counts and exact rule attribution but no raw IP, network hash, device fingerprint, or linked-account list; ordinary login does not produce it. Review requires `admin:risk`, a reason, confirmation, and the expected version; terminal decisions cannot be overwritten and every decision appends risk plus Admin audit evidence.
- Open task disputes have a separate `admin:tasks` operations boundary. An exact-version confirmed decision either accepts the disputed delivery with one balanced Local Test settlement or cancels without settlement; participant notifications, append-only task history, and Admin audit commit atomically.
- Model routes are immutable per creation mode. New generations and retries retain the exact active route revision/version, while unverified external Provider profiles cannot be activated.
- Platform availability changes are immutable atomic revisions. New registrations, generations, publications, checkouts, and task publications check the active revision inside their business transaction and return localized `feature_disabled` errors when paused.
- Public search reads live PostgreSQL facts through targeted normalized pattern indexes. Ranking changes begin as immutable candidates, must pass deterministic offline relevance evaluation, and can receive only 0/5/10/25/50/100 percent of normalized-query cohorts before atomic promotion. Responses identify the policy version and serving variant; every candidate, evaluation, rollout, or index operation requires `admin:ranking`, reason, confirmation, and immutable audit/operations evidence.
- Resource ownership is enforced in Go for generations, private assets, and publishing.
- Published media is public; private generated media requires its owner session.
- API errors include stable codes, safe messages, retryability, and request IDs.
- Request IDs are syntax-bounded, logged with matched route/status/bytes/duration, and retained in body-free request observations for seven days. Admin diagnostics summarize a 15-minute request window plus aggregate 24-hour job-attempt, renewal, expiry, and terminal-failure evidence without exposing request or job payloads.
- Audit events form one database-generated immutable SHA-256 sequence. Admin diagnostics recompute predecessor/event hashes and compare the resulting head with the stored chain state.
- Mutation and worker state live in PostgreSQL rather than process memory. Every job claim has an unguessable fencing token and append-only attempt row; workers renew at one third of the lease, stale owners cannot complete replacement attempts, and expired leases recover atomically with safe error codes instead of raw Provider errors.
- AI disclosure, license, prompt visibility, and source relationships remain attached to published work.
- Task mutations use resource-level authorization and idempotency keys; task transitions, cancellation reasons, and review evidence are durable.
- Accepted task settlement is atomic and balanced. It is labeled Local Test USD in the UI and never represents real funds.
- Product checkout requires explicit acceptance of the displayed license version. Product and license evidence is snapshotted on the order.
- Purchased Assets retain origin and seller provenance. Direct standalone publication is denied; Create requires an active entitlement with derivative rights.
- Local Test refunds enforce the snapshotted refund window, revoke the entitlement, and record balanced reversal entries without moving real funds.

Legal copy, payment terms, tax availability, production privacy review, retention policy, and copyright operations require dedicated acceptance before a production release.
