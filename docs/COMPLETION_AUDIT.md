# HCAI CHAT completion audit

Last updated: August 19, 2026 (CP-73).

This is the active CP-73 audit against the original goal's eleven completion conditions. `verified` means reproducible local evidence exists. `audit_active` means the evidence is substantial but the final requirement-by-requirement review is still open. `external_acceptance` means the local fail-closed boundary exists and production completion requires authority or infrastructure outside this workspace.

| # | Completion condition | Status | Current evidence | Remaining acceptance |
| --- | --- | --- | --- | --- |
| 1 | One documented local start command keeps Vue, Go, PostgreSQL, worker, and media healthy | verified | `make dev`; live Web `:5173`, API `:8080`, `/health`, and `/ready`; PostgreSQL-backed full suite; CP-40 real-process worker restart drill; CP-53 container runtime baseline; CP-56 fail-closed production configuration preflight; CP-57 restarted-source API/Worker and media runtime proof | Production multi-host process supervision is external |
| 2 | Three core workflows pass end to end with explicit Local Test payment/Provider boundaries | verified | `remix-publish`, `task-workflow`, and `product-purchase` Chromium workflows; deterministic Chat/Image/Video/Music plus default-off, offline-contract-tested OpenAI Chat/Image, BytePlus Seedance Video, MiniMax Music, and Stripe payment runtimes. `make payment-drill` proves payment fulfillment/refund plus Connect account creation, Account Link renewal, and signed `account.updated` verification; `make provider-drill` proves Admin-enabled real API/worker Chat/Image generation, typed outputs, Assets, charges, immutable reported usage evidence, and duplicate boundaries in isolated schemas; CP-59 adds an exact two-call, default-off OpenAI staging smoke command; CP-61 adds offline Video/Music HTTP fixture contracts and MP4/MP3 output validation | Approved execution of AI staging calls, Video/Music rights/licensing, application-level external canary evidence, and Stripe merchant authorization are external |
| 3 | No user/Admin dead controls | verified | Route and command-surface inventory found no fixed `disabled=true`, empty `#` navigation, TODO, or coming-soon controls; every mutation remains API/state-backed. Public task/product browse and detail routes now remain usable without a session, while sign-in/registration CTAs return to the selected item and every personal inventory or mutation remains server-authenticated | Recheck when adding routes or commands |
| 4 | Complete en-US and core zh-CN without hard-coded mixing or format drift | verified | Locale-tree key/interpolation parity, Vue static-copy AST audit, stable API-error localization, Chinese mobile/desktop E2E | Production linguistic review is external |
| 5 | Go tests/static checks, frontend checks/build, and Playwright pass | verified | CP-61 passes Go tests/vet/build, frontend typecheck/lint/unit/build, focused Video/Music Provider fixtures, and CP-73 re-runs Go tests/vet, frontend unit/typecheck/lint/build, focused accessibility/creation Chromium `6/6`, and the complete Chromium suite `58/58`; isolated loopback fixtures prove Provider usage/reconciliation and payment/Connect replay boundaries | Re-run Chromium after any user-facing or runtime behavior change |
| 6 | OpenAPI matches implementation; empty database migrates/seeds; rollback is safe or explicit | verified | Generated Vue schema and HTTP contracts include Provider usage/reconciliation, MP3 media, plus full and single-Range Asset delivery; migrations through `0048` are applied to development PostgreSQL and have paired down files; a checksummed backup and isolated restore rehearsal reaches the prior media boundary, while `0048` has a paired profile-only rollback | Scheduled off-host production database/media backup and rollback acceptance remain external |
| 7 | Desktop/mobile, themes, WCAG 2.2 AA, reduced motion, and no overlap pass | verified | Required viewports, strict critical-route axe light/dark gate including anonymous task/product lists and details, reduced-motion/keyboard paths, disabled creator-payout mobile coverage, and CP-73 full Chromium `58/58` pass. The creation surface now passes its light-theme contrast gate and popover-overlay interaction check, plus the 390x844 no-overflow check. The 390px payout boundary asserts exact document width and no misleading onboarding action | Representative VoiceOver/NVDA production acceptance remains external |
| 8 | README documents architecture, startup, tests, migrations, seed, Providers, i18n, and security | verified | README contains all named sections plus recovery, payment, OpenAI runtime drills, the production container baseline, backup/restore, low-cardinality observability/alert checks, safe production configuration preflight, local/S3 media plus deterministic/HTTP scanner boundaries, and separate adapter-media, application-media, and OpenAI staging acceptance commands; policy reachability; configuration; test-mode and production gates | Recheck when runtime or integration contracts change |
| 9 | DESIGN and migration matrix match reality with explicit dispositions | verified | `DESIGN.md` remains the implementation baseline; ADRs 0027/0028 record creation-provider boundaries, ADR 0029 records the default-off Stripe boundary, and the migration matrix records merchant activation separately `externally_blocked` | Recheck when source parity or production scope changes |
| 10 | Progress records final commands/results and external work | verified | `docs/PROGRESS.md` records through CP-73, including Provider/payment/reconciliation, trusted proxy, container, backup/restore, metrics/alert-check, production-preflight, storage/scanner, Video/Music plus OpenAI staging acceptance gates, the creation-surface redesign and fixes, full `58/58` E2E, runtime health, and isolated-account deletion evidence | Continue appending future checkpoints |
| 11 | Development services run at user-accessible local URLs | verified | Web `http://127.0.0.1:5173`; API `http://127.0.0.1:8080`; health and readiness currently return success | Keep services running through handoff |

## CP-73 disposition

The final browser gate after the creation-surface redesign found and closed two regressions: a WCAG contrast failure in the view switcher under the light theme, and a transient asset/mask picker that remained over the newly created result and intercepted its actions. Explicit theme-safe colors and submit-time popover dismissal now keep the surface accessible and operable. Focused `6/6` and complete Chromium `58/58` pass. The overall goal remains active for explicit user acceptance and external production integrations; it is neither stalled nor marked blocked.

## CP-51 disposition

All eleven original completion conditions have reproducible local evidence through CP-73. The local product audit is complete; production-only capabilities remain fail-closed and require the external acceptance listed below and in `docs/MIGRATION_MATRIX.md`. The overall Codex goal remains active for explicit user acceptance and the next production-readiness checkpoint; it is neither stalled nor marked blocked.

## CP-52 disposition

The trusted-proxy boundary is locally implemented and verified. `TRUSTED_PROXY_CIDRS` is explicit, non-zero-CIDR-only, and fail-closed; HTTP and configuration tests cover trusted, untrusted, and malformed forwarding cases. Full Go, frontend, build, provider, payment, and 52-test Chromium gates remain green. Production proxy sanitization, network-evidence retention, privacy notice/legal basis, and deployment acceptance remain external.

## CP-53 disposition

The production container runtime baseline is implemented and verified. API, Worker, migration, and Web images build successfully as non-root processes; the Web proxy routes API and readiness traffic; Compose requires external production configuration and keeps paid integrations disabled by default. This does not satisfy external deployment, backup, observability, legal, or credential acceptance.

## CP-54 disposition

Database backup and restore tooling is implemented and verified against the current PostgreSQL schema. A custom-format archive is atomically written with a checksum sidecar, and the latest temporary database restore reaches migration `0042` with all 42 migration rows present. The scripts intentionally require isolated-target restore confirmation and do not claim off-host retention, media backup, RPO/RTO, or production rollback acceptance.

## CP-55 disposition

The local observability boundary is implemented and verified. `GET /metrics` exposes bounded Prometheus-compatible database, audit-chain, HTTP status-class/duration/response-size, durable queue, oldest-runnable-queue-age, and 24-hour attempt-state series without paths, query values, request IDs, users, prompts, email, Provider bodies, or credentials. `make metrics-check` fails closed on unavailable metrics, database/audit failure, stale runnable work, or excessive failed attempts. Future scheduled work is excluded until `available_at`; the existing email-expiry Job is therefore represented in queue totals without triggering a false stale-work alert. External scraping and retention, paging, distributed tracing, release telemetry, and incident response remain production acceptance work.

## CP-56 disposition

The production configuration boundary now fails before database or Provider access when PostgreSQL lacks verified TLS, the public Web origin is unsafe, deployment placeholders remain, local-only runtime flags are enabled, encryption material is missing, or one key is reused across independent domains. `make production-config-check` exercises the same runtime validation without network access and emits only a safe mode/count summary. This reduces deployment misconfiguration risk but does not supply secrets, infrastructure, external integration approval, or public release acceptance.

## CP-57 disposition

The production media boundary is implemented and locally verified. API and Worker share a configured local/S3 catalog and deterministic/authenticated-HTTP scanner selection; generated and uploaded Assets retain backend/key evidence, clean-only delivery supports one validated byte range, and account deletion removes owned objects through their recorded backend. Migration `0042` is applied, the checksummed restore rehearsal reaches all 42 migrations, current code gates and Chromium `52/52` pass, and a restarted-source isolated-account workflow proves upload, scan, full and Range delivery, generation storage, object deletion, receipt creation, and session revocation. Approved S3/scanner services, encryption/lifecycle policy, staging credentials, outage recovery, scheduled off-host backup, and cross-account infrastructure acceptance remain external.

## CP-58 disposition

The production media adapter now has an executable, default-off staging acceptance command. It refuses execution without an exact one-shot confirmation and validated S3/HTTP scanner modes, uses one random disposable text object, verifies create-only storage, stat, full and Range reads, authenticated content-bound clean scanning, deletion, and post-delete absence, and attempts cleanup after failures. Safe output excludes bucket, endpoint, raw object key, tokens, credentials, and bytes. Local lifecycle, cleanup, unsafe-input, fail-closed CLI, full Go/frontend/static/build gates pass. Selecting approved services, injecting least-privilege credentials, authorizing the call, and retaining a successful staging result remain external.

## CP-59 disposition

The bundled OpenAI Chat/Image adapter now has an executable, default-off staging smoke command. It refuses execution without an exact staging environment, official endpoint, enabled paid runtime approval, disabled local Provider mode, fixed Image size, one-shot confirmation, and exact two-call ceiling. An approved run uses fixed non-user prompts for exactly one Chat and one Image request, requires usage evidence, validates Image dimensions, and emits only bounded model/media/dimension/usage metadata under a three-minute deadline. Local unit, CLI fail-closed, Go/frontend/static/build gates pass without an OpenAI request or fee. Selecting an approved staging project, injecting credentials, authorizing the two paid calls, retaining the result, and completing application-level canary/cost/policy/disable evidence remain external.

## CP-60 disposition

The production media boundary now has a second executable, default-off application staging command in addition to the adapter lifecycle check. It requires a separately supplied disposable staging database, creates and drops one isolated acceptance schema, and refuses unsafe schema, environment, adapter, or local-Provider modes. The command exercises HTTP upload, pending isolation, one durable scan attempt, clean full/Range delivery, cross-account denial, Job/audit/notification evidence, and object deletion while emitting only bounded safe facts. An isolated PostgreSQL/local-adapter contract test and the pre-access confirmation gate pass. Selecting approved S3/Scanner services, credentials, and a staging database, authorizing the external calls, and retaining a successful run remain external.

## CP-61 disposition

BytePlus ModelArk Seedance Video and MiniMax Music 3.0 now have default-off Go Provider runtimes in the shared exact-capability catalog. Video creates and polls an asynchronous task within bounded route timeout, downloads and validates the short-lived MP4 result, and accepts only clean owned image references. Music submits the official Music 3.0 contract, downloads and validates an MP3 result, and persists it through the existing generation Asset and Local Test billing transaction. Migration `0048_video_music_provider_runtimes` updates the disabled profiles with paired rollback. Configuration tests cover independent approval/credential gates, loopback fixture endpoints, official production endpoint restrictions, and bounded polling. Offline fixture tests cover request shape, polling, output signatures, authentication failure classification, and upstream-body exclusion. No external call or credential was used; selecting rights, models, credentials, and staging approval remains external.

## CP-62 disposition

Video/Music now have a dedicated default-off staging acceptance command in addition to their offline adapter contracts. `RunVideoStagingAcceptance` and `RunMusicStagingAcceptance` each make one typed media call and emit only bounded metadata. `cmd/creativeprovidercheck` requires `APP_ENV=staging`, the exact official endpoints, both paid-call approvals, disabled Local Test runtime, an exact confirmation phrase, and one-call ceilings before any runtime is loaded. Submission validation now applies the exact reviewed capability of each external route: unsupported 30-second Video requests, non-image Video references, MiniMax audio references, and current OpenAI Image references or masks are rejected before a job or billing hold is created, while Chat documents are compiled into bounded text context for the model. Local Test retains its broader deterministic capability range. No external or paid call occurred.

## CP-63 disposition

`GET /api/v1/creation/capabilities` now exposes a non-secret projection of each active route's supported parameters. The Vue studio uses this projection to render only the active Provider's durations, output formats, reference kinds, and Mask support, while retaining a deterministic fallback during capability outages. The endpoint and generated schema are covered by HTTP tests; the creation studio's four-mode browser workflow remains green. Provider credentials, paid calls, and external activation remain unchanged and fail-closed.

## CP-64 disposition

When a capability projection marks a mode unavailable, the creation studio now presents a localized status notice and disables submission before any billing or generation request. The fallback remains available if the capability request itself is temporarily unavailable, while the server remains authoritative. Full local verification passed without external calls.

## CP-65 disposition

Creation capabilities now distinguish requested formats from Provider result formats. The MiniMax projection can advertise MP3 output while retaining the reviewed request contract; Local Test continues to report WAV. The frontend summary uses the actual result format and hides a single fixed format selector, preventing a false impression of user choice.

## CP-66 disposition

The Video/Music staging CLI's safety gates now have direct unit coverage for exact confirmation, one-call ceilings, staging-only execution, disabled Local Test mode, official endpoints, credentials, models, and paid-call approvals. Tests exercise only pure validation and cannot trigger network access.

## CP-67 disposition

An HTTP contract test now activates a loopback-configured MiniMax Music route in an isolated database and verifies the public capability response without invoking the Provider. The projection reports MP3 results and no audio reference support, proving Admin route activation and UI capability alignment are connected before credentials are introduced.

## CP-68 disposition

The creation studio now treats a missing mode entry in an otherwise successful capability response as unavailable. Only a failed capability request uses the local fallback; malformed or incomplete route data fails closed at the submit control.

## CP-69 disposition

The OpenAI staging command now has direct unit coverage for its exact confirmation-adjacent configuration boundary: staging mode, disabled Local Test, official endpoint, enabled paid approval, credentials/models, and fixed image dimensions. The tests remain offline and do not construct a network client.

## CP-70 disposition

The creation studio now validates the shape of every successful capability projection before exposing its controls. Missing arrays or mode-specific parameters fail closed instead of silently falling back to Local Test controls; an unavailable capability remains visible with a disabled submit action. A focused browser path clicks every creation mode and proves the route, active navigation state, and mode-specific content update in place. No external or paid Provider call occurred.

## CP-71 disposition

Generation retries now revalidate the exact capability of the currently active Provider route. A route changed after the original submission can no longer accept an unsupported parameter or reference and reserve credits before the worker discovers the mismatch. The integration evidence switches a Local Test Video generation to a BytePlus route with a narrower duration contract and proves the invalid retry creates no new hold or Job.

## External acceptance boundaries

- Approved real AI Provider credentials and paid-call authorization.
- Stripe merchant credentials and legal scope, test-to-live checkout/Webhook/reconciliation proof, Connect/KYC destination onboarding, payout, dispute, tax, and invoice acceptance.
- Production OAuth callbacks, email delivery plus bounce/complaint handling, object storage/scanner, and deployment.
- Production proxy trust, network-evidence retention, privacy notice/legal basis, copyright operations, regional policy, backups, external Provider deletion, centralized telemetry, alerts, and incident response.
- Representative assistive-technology and production linguistic/legal acceptance.
