# Provider integration guide

This guide is the acceptance contract for connecting a real Chat, Image, Video, or Music Provider. It does not authorize paid calls or the storage of credentials in this repository.

## Deployment proxy boundary

Set `TRUSTED_PROXY_CIDRS` only to the deployment proxy networks that sanitize and replace `X-Forwarded-For`. The API uses a forwarded client address only when the direct TCP peer matches one of those non-zero CIDR prefixes; otherwise it uses the TCP peer address. An empty value is fail-closed for forwarded headers. Account-link network evidence is immediately reduced to a one-way hash and does not retain the raw address. Proxy configuration, retention, privacy notice/legal basis, and production acceptance remain external deployment responsibilities.

## Runtime implementation

1. Implement `creation.ProviderRuntime` in `internal/creation` or a Provider-specific internal package.
2. Return a stable Provider identifier matching `provider_profiles.provider`.
3. Restrict `Supports` to exact reviewed mode/model combinations.
4. Keep API keys, signing secrets, account identifiers, and upstream request IDs inside the adapter. Never include them in `ProviderRequest`, errors, audit metadata, or generated Asset metadata.
5. Honor request-context cancellation and deadlines. Do not start detached polling or downloads after the context is cancelled.
6. Return upstream failures through `creation.NewProviderFailure`; unclassified errors are reduced to `provider_request_failed` so raw Provider responses never enter user-visible generation evidence.
7. Map upstream results to the closed `ProviderOutput` contract. The creation service owns atomic local persistence and downstream Asset creation.

## Configuration and activation

1. Load credentials from the deployment secret manager through environment-specific configuration.
2. Construct and register the runtime only when every required value validates. An absent or malformed credential must leave the runtime unavailable.
3. Add or update the Provider profile with a reviewed model name and conservative estimated cost. Keep `admin_enabled=false` initially.
4. Verify that Admin reports `runtimeAvailable=true` before enabling the profile.
5. Activate a new immutable model-route revision with reviewed timeout and retry limits. Existing generations must retain their original route evidence.

## Bundled OpenAI Chat and Image adapter

The repository includes a standard-library HTTP adapter for two exact capabilities:

- Chat: `POST /v1/responses`, default model `gpt-5.6-terra`, `store=false`, and bounded `max_output_tokens`. The adapter traverses every response item and collects every `output_text` content part.
- Image: `POST /v1/images/generations`, default model `gpt-image-2`, one PNG result, reviewed size/quality, automatic moderation, bounded base64 decoding, and actual PNG dimension inspection.

The implementation follows the official [text generation](https://developers.openai.com/api/docs/guides/text) and [image generation](https://developers.openai.com/api/docs/guides/image-generation) contracts. It uses no SDK and does not send an external request in tests.

### Bundled BytePlus Video and MiniMax Music adapters

The repository also includes default-off adapters for the two non-image creation modes:

- Video: BytePlus ModelArk Seedance uses `POST /api/v3/contents/generations/tasks`, bounded status polling through `GET /contents/generations/tasks/{id}`, and immediate MP4 download from `content.video_url`. The adapter validates task state, restricts output hosts to the official BytePlus object-storage domain (or a loopback fixture), bounds the downloaded body, and accepts only an MP4 `ftyp` signature. Clean owned image references are passed as bounded Base64 `image_url` content; unpersisted or unavailable references fail closed.
- Music: MiniMax Music 3.0 uses `POST /v1/music_generation` with `output_format=url`, downloads the returned MP3 from the approved MiniMax/file-CDN host, bounds the body, and accepts only an ID3 or MPEG-frame signature. The adapter does not persist upstream URLs or response bodies.

Both adapters are registered only when their independent `*_ENABLED`, `*_PAID_CALLS_APPROVED`, credential, model, and endpoint checks pass. Development HTTP is loopback-only; production endpoints are fixed to `https://ark.ap-southeast.bytepluses.com/api/v3` and `https://api.minimaxi.com/v1`. Video polling is bounded by `VIDEO_TIMEOUT_SECONDS` and `VIDEO_POLL_INTERVAL_SECONDS`. External Video/Music rights approval, credentials, and staging acceptance are still required before Admin can enable their profiles.

The adapter is unavailable by default. Both API and worker processes register it only when all of these conditions pass configuration validation:

1. `OPENAI_ENABLED=true`.
2. `OPENAI_PAID_CALLS_APPROVED=true`.
3. `OPENAI_API_KEY` is present.
4. The model, output, and endpoint settings are valid.
5. Production uses exactly `https://api.openai.com/v1`; non-production plain HTTP is loopback-only.

Registration still does not route traffic. Migration `0036_openai_chat_image_runtime` keeps both OpenAI Provider Profiles disabled. An authorized administrator must explicitly enable the reviewed profile and activate a new immutable Chat or Image route before submission can reserve credits or enqueue work.

Stable failure mapping is intentionally body-free:

| Upstream condition | Stable class | Retry |
| --- | --- | --- |
| `401` / `403` | `provider_authentication` | no |
| content policy / `image_generation_user_error` | `provider_content_rejected` | no |
| other user/request `4xx` | `provider_invalid_request` | no |
| `429` | `provider_rate_limited` | yes, bounded `Retry-After` |
| timeout / network failure | `provider_timeout` / `provider_request_failed` | yes |
| `5xx` | `provider_unavailable` | yes, bounded `Retry-After` |
| malformed, empty, oversized, or wrong-media success | `provider_response_invalid` | no |

Upstream response bodies, messages, request IDs, API keys, organization IDs, and project IDs never cross into generation errors, durable job evidence, audit metadata, or API responses.

## Usage and cost evidence

Each successful generation writes exactly one immutable, owner-scoped usage record. The record contains only the configured Provider/model, a `reported` or `not_reported` status, bounded input/cached-input/output/reasoning/total token counters when returned, and a timestamp. It does not retain an upstream request ID, request/response body, prompt copy, credential, organization, or project identifier.

- `estimatedCostCents` and `chargedCostCents` are HCAI Local Test accounting facts. They are not an assertion of external Provider spend.
- `providerUsage` is an operational metering fact returned by a successful Provider response. A Local Test runtime, or a Provider that omits usage, is explicitly recorded as `not_reported`.
- Actual Provider invoice reconciliation requires an approved billing export or invoice feed, a reviewed attribution key, a defined reconciliation period, and an overage policy. It is a staging/production acceptance activity and is not implemented by token evidence alone.

The owner-facing Generation API and Workspace show the token counters only when the Provider reported them and state this boundary directly. Migration `0040_generation_provider_usage` enforces the record shape and rejects updates or deletes.

The pre-credential aggregate reader in `internal/reconciliation` follows the official organization Costs API contract: it requests bounded UTC daily buckets, filters the configured project in memory-only configuration, follows opaque pagination, converts fixed decimal amounts to integer micros, and returns no project or line-item identifiers. It is registered only when a separate reconciliation approval gate is enabled; generation traffic remains independently disabled.

Aggregate reconciliation is durable rather than a synchronous Admin fetch. Migration `0041_provider_cost_reconciliation` stores only the Provider/currency, exact UTC period, aggregate Provider cost, local estimate, aggregate reported token evidence, variance, immutable threshold snapshot, status, stable failure code, requester, and job reference. It never stores a project ID, line item, raw Provider response, credential, request ID, or per-generation invoice allocation. `admin:finance` must submit a confirmed request with a bounded reason; that API transaction writes audit evidence and one `provider.cost.reconcile` job. The Worker finalizes the period as `matched`, `overage`, or `failed`; finalized evidence and request evidence are database-protected from mutation.

## One-shot OpenAI staging smoke check

The repository includes `make provider-staging-check` as a default-off, operator-authorized smoke check for the bundled OpenAI Chat/Image adapter. It is not part of application startup, CI, `make test`, `make build`, or `make provider-drill`, and the repository never runs it automatically.

An operator must first approve the staging project, model access, budget, region, retention behavior, and the two paid calls. Inject the normal OpenAI staging configuration through the secret manager, then run:

```bash
APP_ENV=staging \
PROVIDER_ACCEPTANCE_CONFIRM=I_APPROVE_OPENAI_STAGING_CALLS \
OPENAI_ACCEPTANCE_MAX_CALLS=2 \
make provider-staging-check
```

The command exits before a request unless every boundary is satisfied: `APP_ENV=staging`, `OPENAI_ENABLED=true`, `OPENAI_PAID_CALLS_APPROVED=true`, `LOCAL_PROVIDER_ENABLED=false`, the exact official `https://api.openai.com/v1` base URL, an exact registered Chat and Image capability, a fixed non-`auto` image size, the exact confirmation phrase, and the exact maximum-call value `2`. A three-minute context bounds the complete run.

An approved run performs exactly one fixed Chat request and one fixed Image request. Neither prompt contains user data. It requires Provider usage evidence for each response, validates the Image media contract and configured dimensions, and returns only the Provider, paid-call count, model, MIME type, output byte count, dimensions, and bounded token counters. Prompt text, Chat output, Image bytes, credentials, organization/project identifiers, upstream request IDs, and raw response bodies are neither printed nor persisted.

Passing this smoke check proves only that the configured staging credential can execute one typed Chat and one typed Image request. The application-level timeout, explicit cancellation, bounded retry/rate-limit behavior, content-policy mapping, durable success/idempotency boundary, actual-cost reconciliation, deletion handling, and Admin canary-disable checks below remain separate release evidence.

## Required staging evidence

- Successful request and typed output for every enabled mode/model.
- Context timeout, explicit cancellation, rate limit, authentication failure, invalid response, and upstream 5xx behavior.
- Retry count matches the immutable route revision and does not duplicate Provider calls after a durable success boundary.
- Exactly one HCAI Asset, billing capture, notification, audit event, and Webhook event per successful generation.
- Exactly one immutable minimized usage record per successful generation; validate reported counters for every enabled Provider and explicit `not_reported` evidence when the Provider supplies no meter data.
- Failed or cancelled generations release held credits and leave no accessible or orphaned output.
- Estimated and actual Provider cost reconciliation is within the approved policy; unexpected overage fails closed.
- Provider content-policy decisions are translated to stable safe error codes without storing prohibited prompts or raw upstream responses in logs.
- A controlled canary can be disabled through Admin without changing or corrupting historical generations.

### One-shot Video/Music staging check

`make creative-provider-staging-check` is the default-off acceptance command for the two non-image modes. It requires `APP_ENV=staging`, `LOCAL_PROVIDER_ENABLED=false`, the exact official BytePlus and MiniMax endpoints, both `*_ENABLED=true`, both `*_PAID_CALLS_APPROVED=true`, non-empty credentials/models, the exact confirmation `CREATIVE_PROVIDER_ACCEPTANCE_CONFIRM=I_APPROVE_CREATIVE_STAGING_CALLS`, and `VIDEO_ACCEPTANCE_MAX_CALLS=1` plus `MUSIC_ACCEPTANCE_MAX_CALLS=1`.

An approved run performs exactly one five-second 16:9 Video request and one 30-second Music request through the real adapters. The adapter owns asynchronous Video polling and short-lived URL download; the command sees only typed MP4/MP3 output. Safe JSON contains Provider, mode, model, call count, MIME type, output byte count, and Video dimensions. It excludes prompts, media bytes, URLs, credentials, request IDs, and upstream response bodies. The command is never invoked by startup, CI, `make test`, `make build`, or local drills, and no external or paid call has been made by repository verification.

Before credentials are introduced, run `make provider-drill`. It starts real API and worker binaries with a loopback OpenAI fixture in an isolated PostgreSQL schema, enables the configured Chat/Image profiles through the Admin API, activates immutable routes, and proves one typed text Asset plus one typed PNG Asset, Local Test billing, and idempotent replay. The drill is pre-credential evidence only; it does not contact OpenAI or replace staging acceptance with the approved tenant.

## Release authority

Production activation requires explicit approval for the selected Provider, models, regions, retention behavior, budget, and paid-call ceiling. Until that approval exists, keep `OPENAI_ENABLED=false`; the deterministic `local_test` runtime remains the only registered generation runtime.
