# HCAI CHAT

Manual wallet adjustments now require operator-scoped `Idempotency-Key` commands (migration 0135), atomically linked to ledger and audit evidence. Same-key retries cannot apply the amount twice, replays require current finance permission, and old untracked writers are rejected. Coordinate migration/API/frontend/data-export worker; only isolated environments were upgraded. Browser keys survive same-tab reload/reauthentication until success, not loss of tab storage or cross-device actions. This does not implement seller settlement or external funds movement. See [scope, regression and deployment limits](docs/resource-marketplace-flows.md#11135-手工额度调整幂等与重试链路0135).

Operator authority is now checked in the same SQL statement that enqueues media cleanup, export and account-deletion recovery. Committed role revocation or suspension during lock waits or before enqueue cannot create a replacement job; original failures and evidence remain. Request recovery explicitly uses Read Committed for fresh permission checks. No migration is added and running services remain unchanged. See [concurrent revocation and regression evidence](docs/resource-marketplace-flows.md#11131-恢复入队时的运营权限与支付回归闭合).

Production image checks now cover the compiled Go executable's architecture, including both AMD64 and ARM64 CI builds, and reject stale Nginx proxy test images. The web image baseline is Nginx 1.30.5. Recovery failure metrics avoid expanding full retry-permission policies; the 11.129 HTTP race regression passes with the original two-second metrics deadline. These changes are not deployed and do not complete seller settlement or real-provider acceptance. See [image and metrics verification](docs/resource-marketplace-flows.md#11129-运行镜像架构代理版本与指标查询).

HCAI CHAT is an international-first AIGC creation network being rebuilt with Go, Vue 3, and PostgreSQL. The product connects discovery, creation, owned assets, publishing, community participation, and transparent digital commerce.

The target experience is `en-US` first. Core flows are also localized for `zh-CN`. The source project at `/Users/helong/Work/newchat` is migration input and must remain read-only.

## Current checkpoint

The runtime build now requires Go 1.26.8 and updated chi, pgx and Go crypto/text/system dependencies to address known advisories. `make security` checks imported Go packages and production npm lockfile dependencies, and CI runs it independently. Execution recovery now reserves its binding before trying account/entity locks so concurrent passes cannot postpone an in-flight scan or generation recovery. These changes are not deployed; see [recovery and dependency verification](docs/resource-marketplace-flows.md#11103-扫描与生成恢复不能互相推迟在途执行).

Migration 0134 keeps original pending refunds on evidenced historical closed Stripe product orders in the existing automatic reconciliation queue. It requires the saved recovery marker and matching order ownership/product, and preserves shared backoff, original-merchant checks, active-dispatch exclusion, failed-job recovery and concurrent scheduling guards. Reads can eventually confirm the original full refund through 0133; no new refund operation is created. Existing history, audit, owner export and backlog metrics apply. Downgrade refuses unresolved closed refund operations/read gaps; an allowed rollback restores the earlier view without deleting evidence. Coordinate migration/API/worker; running services and databases remain unchanged. See [rules](docs/resource-marketplace-flows.md#713-历史关闭订单的未决退款自动跟进0134) and [verification](docs/resource-marketplace-flows.md#11128-历史未决退款自动跟进与最终确认).

Migration 0133 confirms already-executed original refunds on historical closed Stripe product orders. A registered, complete post-recovery query must prove exactly one original full successful refund, reconcile all known operations, and leave no active refund dispatch or unresolved financial evidence. Immutable confirmation binds the original operation, query and event; shared completion marks the order/payment refunded, revokes existing rights and resumes cleanup after the original paid event finishes. It creates no refund operation or outgoing refund command. Buyer-only export includes safe confirmation lineage. Any confirmation blocks downgrade; an empty rollback restores 0132 without deleting evidence. Coordinate migration/API/worker after draining old payment and cleanup executions. Only isolated schemas and loopback providers were used; running services remain unchanged. Manual/partial/duplicate funds disposition, settlement and production acceptance remain incomplete. See [rules](docs/resource-marketplace-flows.md#524-已关闭历史订单的原退款确认0133) and [verification](docs/resource-marketplace-flows.md#11127-历史关闭订单的已发生退款确认).

Migration 0132 preserves complete saved paid Checkout evidence for historical cancelled/payment-failed Stripe product orders. It blocks another checkout and retains delivery evidence, binds the original paid event/job without reopening the order, and schedules an authenticated refund-history preflight before creating any compensation obligation. Only a complete read requested and started after the recovery marker, with no unresolved funds, prior local refund obligations or historical entitlements, may queue the original event for the existing compensation/refund workflow. Unknown/manual/partial evidence remains held; finance can retry history reads with existing role/version/active-job guards. Missing checkout scheduling and its deferred binding trigger accept qualified closed orders without resetting failed jobs. Owner export includes safe recovery lineage; seller export excludes it. The `closed_checkout_paid` live/test problem metric tracks unfinished closed recovery, then existing refund metrics track compensation. Shared retention reads recovery progress; full funds eligibility stays at the processing boundary to avoid repeatedly expanding the complete refund policy in metrics. Drain old payment/cleanup executions and coordinate migration/API/worker/checker. Down refuses any saved recovery marker or unresolved closed paid state; it never deletes financial evidence. Only isolated schemas were migrated, with no live-provider calls or service restart. This does not complete historical refund adjudication, settlement, historical delivery migration or production acceptance. See [rules and verification](docs/resource-marketplace-flows.md#523-已关闭历史订单的收款恢复与退款前核对0132).

Migration 0131 preserves disagreements between authenticated paid Checkout queries, session lookups and merchant recovery receipts. Queries persist their original result before returning reconciliation failure; subsequent checks and queued fulfillment cannot ignore the conflict. Consistent additional sources are merged under the payment lock instead of stalling an existing check. Shared finance attention, ordinary refund restrictions and delivery retention include unresolved conflicts, while normal finance recovery and missing-check scheduling exclude them. The bounded `checkout_evidence_conflict` problem metric reports them independently of job status; ship the matching alert checker. Drain old payment executions and coordinate migration/API/worker/checker. Downgrade refuses to remove protection while conflicts exist; a conflict-free rollback preserves all receipts. Only isolated schemas were migrated. This is not complete financial adjudication, historical-order repair or production acceptance. See [rules and verification](docs/resource-marketplace-flows.md#522-并发付款证据的一致性与持久冲突保护0131).

Migration 0130 unifies authenticated terminal checkout evidence from session lookup and merchant identity recovery for scheduling, finance eligibility and missing-evidence metrics. Workers revalidate complete saved paid Checkout observations and current bindings before normal fulfillment, including original/replacement jobs and missing-check recovery without an available provider runtime. Conflicting sources remain unreconciled; unpaid recovery still requires an authenticated remote read. Owner exports preserve safe recovery/check lineage. This never manufactures the original checkout request or replaces subsequent historical refund checks. Coordinate migration/API/worker/frontend after draining old payment executions. Down restores the previous scheduling view and removes the shared projection without deleting receipts; stop programs that depend on the new view before rollback. Only isolated schemas were migrated. PaymentIntent-only recovery, previously cancelled orders, unsaved responses and production acceptance remain outside this checkpoint. See [rules and verification](docs/resource-marketplace-flows.md#521-商户恢复收款证据的履约交接0130).

Migration 0129 preserves independent checkout lookups that share one active check. The matching worker consumes authenticated saved paid observations without another provider read, revalidates current bindings and schedules normal fulfillment even when the shared check has an older expired response. Invalid or conflicting evidence remains unreconciled; buyer exports include safe lookup/check lineage. Ship migration/API/worker together after draining old payment executions. Shared associations block downgrade; already-cancelled historical orders are not automatically rewritten. Running services remain unchanged. See [rules and verification](docs/resource-marketplace-flows.md#520-已付款定位证据的履约交接0129).

Migration 0128 registers each actual Stripe refund read before remote dispatch, with its original check, execution and absolute deadline. Missing responses join shared financial and media-retention review. A later complete authenticated query can record immutable recovery only after the old read/persistence windows and loss of its live lease; saved positive observations retain their independent gates. Finance history, owner export, existing automatic checks and an overdue-read metric are connected. Ship migration/API/worker/frontend/checker together after draining old processes. Any execution evidence blocks destructive downgrade; old unregistered reads are not backfilled. Isolated verification does not establish live-provider or production acceptance. See [rules](docs/resource-marketplace-flows.md#712-退款查询执行登记与中断恢复0128).

Validated checkout queries, session lookups and merchant identity recovery share a five-second evidence persistence/retry window with refund reads. They preserve the original response through worker cancellation and proven PostgreSQL transaction aborts, without repeating provider reads or financial commands; uncertain commits are not replayed locally. A late expired/unpaid response also rechecks recorded payment evidence under the payment lock, preserving paid orders whose fulfillment is still queued. That persistence-retry checkpoint introduced no new migration; running services remain unchanged. See [checkout cancellation and concurrency](docs/resource-marketplace-flows.md#11121-支付查询取消与待履约收款的并发保护) and [evidence write recovery](docs/resource-marketplace-flows.md#11122-支付只读证据的共享重试与取消保护).

Marketplace migration 0127 retains authenticated late refund observations in immutable receipts attached to the original check and execution. Receipt history uses finance-only, one-record pages; owner exports include safe observations. Shared financial gates and file retention include unresolved receipts without replacing the original result or creating financial operations. Coordinate migration/API/worker/frontend; any saved receipt blocks destructive downgrade. Only isolated environments were migrated. See [rules and verification](docs/resource-marketplace-flows.md#711-被接手查询的迟到回执0127).

Marketplace migration 0126 unifies unsettled-payment retention for ordinary delivery cleanup and account deletion. Pending refunds, failed compensation and all shared financial review conditions protect required delivery bytes even after the buyer loses access; verified resolution lets existing cleanup proceed. Historical contracts without independent copies retain their source. Stop old cleanup instances and deploy migration/API/worker together; the running database remains unchanged. See [retention scope](docs/resource-marketplace-flows.md#634-退款未决时各清理入口统一保留交付0126).

Marketplace migration 0125 preserves unmatched authenticated refund observations across later failed or incomplete reads. Shared refund gates and media retention continue to protect unresolved transactions, and a separate financial metric reports them even without a local refund attempt. Original operation binding plus verified success can clear the gap; no synthetic operations or money movement are introduced. Deploy migration, API, worker and checker together; running environments remain unchanged. See [scope and validation](docs/resource-marketplace-flows.md#79-历史退款查询证据不能被后续失败或遗漏覆盖0125).

Three local vertical workflows are operational:

`Discover work -> Remix -> durable Chat/Image/Video/Music generation -> Asset -> Publish -> Community`

`Discover demand -> proposal or direct accept -> Create with task context -> versioned Asset delivery -> review or revision -> Local Test settlement or dispute`

`Browse product -> confirm offer and license -> external Provider checkout -> verified payment fulfillment -> receive entitlement and Asset -> use within license -> confirmed refund and entitlement revocation`

The product purchase flow is implemented with isolated simulated-Provider regression coverage. Delivery repair supports exact-byte backup uploads up to 100 MiB. Full production acceptance, seller settlement, historical delivery migration, production storage/recovery validation, and some historical-payment recovery paths remain incomplete; see the [Resource Marketplace flow and audit document](docs/resource-marketplace-flows.md).

The four creation modes have deterministic Local Test runtimes plus default-off real Provider boundaries: OpenAI Chat/Image, BytePlus ModelArk Seedance Video, and MiniMax Music 3.0. They require explicit credentials, paid-call approval, typed output validation, and audited route activation; the default development environment never calls an external Provider. `GET /api/v1/creation/capabilities` gives the Vue studio a non-secret active-route capability projection so controls stay aligned when an Admin changes the route.

Chat, Image, Video, and Music use clearly labeled deterministic local providers. Chat stores text on the conversation generation record; Image, Video, and Music produce JPEG, MP4, and WAV Assets. A default-off OpenAI adapter implements Chat through the Responses API and Image through the Image Generation API, but it is not registered without explicit configuration, credentials, and paid-call approval. The owner-scoped Generation Center provides URL-backed mode/status/UTC-date filters, stable cursor pagination, deep-linked evidence, and server-derived cancel/retry/download/reuse actions. Owned Assets, reference-only Community saves, the public Community feed, cross-version Asset-family usage, Community comments, publishing drafts, Marketplace orders, Account sessions, and notification-delivery evidence now have independent bounded stable pagination with duplicate-free continuation. Generation reservations/charges and Admin adjustments use explicitly labeled local ledgers; HTTP task rewards require verified Provider funding and asynchronous payout; the payment boundary additionally supports default-off Stripe Hosted Checkout, signed Webhooks, idempotent event processing, asynchronous refund/transfer recovery, creator payout-destination evidence, and permission-scoped Admin operations. Email identity, sessions, roles, permissions, profile settings, security logout, notifications, cross-domain public search, creator profiles, persisted publishing drafts, Asset version families and downstream usage evidence, Community interaction/governance, private support/copyright intake, immutable cross-domain risk rules and privacy-minimized registration account-link review, immutable model routing, versioned platform availability gates, tamper-evident audit chaining, operational diagnostics, default-off Developer Access, signed Webhooks, deep links, local Asset upload/scanning, and the local data export/deletion lifecycle are active. Production integrations and acceptance boundaries remain in progress; see `docs/MIGRATION_MATRIX.md`.

The current checkpoints include default-off staging acceptance commands for all four creation modes. OpenAI Chat/Image use a two-call check; BytePlus Video and MiniMax Music use a separate one-call-per-provider check. Each requires staging configuration, an exact confirmation phrase, and an explicit call ceiling. The repository never runs these paid checks automatically.

Task and product lists/details are public browsing surfaces. Authentication is required only for personal inventories and mutations such as publishing, proposing, accepting, purchasing, delivering, reviewing, or refunding; sign-in and registration return safely to the selected public item.

## Architecture

- `cmd/api`: Go REST API and embedded OpenAPI document.
- `cmd/worker`: independent worker that claims durable PostgreSQL job leases.
- `cmd/migrate`: idempotent embedded SQL migration runner.
- `internal/testfixtures/cmd/seed`: deterministic fixtures restricted to an isolated test schema; never used by normal development or production startup.
- `internal`: modular domain and platform packages.
- `internal/tasks`: demand briefs, proposals, assignment, commissioner cancellation, versioned delivery, review, dispute, history, and Local Test settlement.
- `internal/marketplace`: product discovery, licenses, public samples, order evidence, and entitlements; external checkout/fulfillment/refund runs through `internal/payments`. The legacy local purchase implementation is removed. Historical internal refunds use a separate evidence-checked adapter that reverses the original payee; missing evidence cannot fall back to a synthetic refund. Order and asset projections depend on migration 0097; see [historical compatibility](docs/resource-marketplace-flows.md#75-旧本地购买退役与历史余额冲回0097).
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

The current Task Marketplace routes, roles, lifecycle, provider funding, delivery grants, dispute recovery, and verification boundaries are documented in [任务广场全链路与验收记录](docs/task-marketplace-flows.md). This implementation review supersedes historical local-ledger descriptions for the current HTTP task workflow.

The current Community publishing, discussion, interaction, reports/appeals, moderation, notifications, and account-data flows are documented in [社区全链路说明与验收记录](docs/community-flows.md), including personal feeds, private discussion drafts, permissions, versioned governance, restoration safeguards, and C01–C13 acceptance evidence.

The Inspiration library browsing, work details, creator profiles, remix-to-publication flow, visibility boundaries, and I01–I10 fixes are documented in [灵感库全链路说明与验收记录](docs/inspiration-library-flows.md).

Seller draft editing, reviewed publication, pause/withdrawal, governance blocks and resubmission are now available for independent single-file USD listings at `/workspace/products` and `/admin/products`. Publication requires migration 0093 and preserves accepted buyer contracts. Private sales, accepted license details and paginated order status history are available at `/workspace/sales`, using migration 0094 and the same historical seller attribution as account exports. Sales reads exclude buyer identities and payment credentials; order amounts do not represent seller settlement or available earnings. Seller settlement remains incomplete.

Start with [资源市场完整链路](docs/resource-marketplace-complete-flow.md) for the current buyer, seller and operator flows, page/API map, implementation boundaries and acceptance checklist. Use [资源市场全部链路交接文档](docs/resource-marketplace-all-flows.md) for detailed handoff, and [资源市场全链路说明与审查记录](docs/resource-marketplace-flows.md) for historical findings, repairs and version-scoped test evidence. Earlier expanded journeys and rules remain in [业务与交接](docs/resource-marketplace-journeys.md), [技术基线](docs/resource-marketplace-current-flow.md) and [全链路总览](docs/resource-marketplace-guide.md); consult the current entry point when their historical conclusions differ. Multi-file contracts, ZIP downloads and repair constraints are covered in [多文件商品交付实现说明](docs/resource-marketplace-bundles.md).

Migration 0124 restores missing Stripe product checkout checks with a bounded startup/periodic scan, immutable dispatch lineage, owner export and operational alerts. It only schedules reads for due identity-backed open orders with no prior check history; failed/cancelled jobs and unresolved events keep their existing recovery paths. Coordinate migration, API, worker and alert checker; running environments have not been upgraded. See [scope and verification](docs/resource-marketplace-flows.md#822-缺失支付会话核对任务的补建0124).

Verified product downloads, copy preparation and repair share a process-wide temporary-file budget: 512 MiB, 16 files and a 64 MiB free-space floor by default. Production Compose supplies a separate 768 MiB memory-backed mount per API/worker container; include it alongside exports and application memory when sizing deployment. Capacity failures remain retryable and do not alone trigger refunds or grant rights. See [staging configuration and limits](deploy/README.md#verified-product-media-staging); multi-instance capacity acceptance remains pending.

Media operators can review missing historical contracts and delivery evidence at `/admin/deliveries`. The permission-scoped, read-only inventory checks bounded batches, preserves continuation through empty result pages, and separates unknown payment environments from verified test/live records. Migration 0111 adds its order-creation index; it does not backfill contracts or migrate files. See [evidence inventory and remaining migration scope](docs/resource-marketplace-flows.md#614-历史交付证据核对入口0111).

Migration 0112 adds real ordered source lists for multi-file product drafts. Sellers can save and reorder 2–20 independently owned files; all members participate in private version checks and original-file protection. Authorized reviewers can inspect individual members, and owner exports include safe file names/IDs. At this stage public sales were gated; subsequent contract, delivery and publication integration is described below. Coordinate the migration, API, export worker and frontend; rollback refuses to discard file-list history. The running database has not been migrated. See [implementation and remaining integration](docs/resource-marketplace-bundles.md).

Migration 0113 freezes all bundle sources into offer/contract evidence and persists a versioned ZIP snapshot with per-file and whole-object checksums before any durable copy write. Internal preparation resumes from frozen locators and verifies uncertain writes; historical members remain private after listing edits, and buyer exports use safe file projections. Single-file hashes and exports stay compatible. This migration alone does not enable public sales. All-member retention, internal accepted-order fulfillment and buyer HTTP authorization are integrated in the subsequent stage below; publication and checkout are integrated in 0115 below. Existing bundle evidence prevents rollback; the running database has not been migrated. See [bundle snapshot scope](docs/resource-marketplace-bundles.md#7-冻结合同与独立-zip-快照0113).

Migration 0114 unifies all frozen order sources and associated accounts for original-media retention, legal holds, ended-hold dispatch and ZIP cleanup. Operators can reconstruct a damaged ZIP from every frozen original or restore an exact stored/uploaded backup; repair rechecks permission after lock waits. Existing bundle evidence prevents rollback to narrower retention. Coordinate API, cleanup/hold workers and generated types; do not mix old workers that only retain the first source. Publication and public checkout are integrated in 0115 below. Internal accepted-order fulfillment now checks every source and grants a ZIP document asset; authorized whole/member downloads and buyer file-list UI are connected, with read-time rights checks and no ZIP-to-model reuse. The subsequent isolated seller-to-payment acceptance and worker protocol checks are described below; the running database is unchanged. See [bundle lifecycle scope](docs/resource-marketplace-bundles.md#8-全部来源留存与整包修复0114).

Migration 0121 adds immutable upload scan-job bindings, commit-time lease verification and bounded recovery of evidenced failed scans to controlled review. Drain old workers before the coordinated API/worker/migration upgrade; old writers are rejected and evidence prevents rollback. The running database has not been migrated. See [scan recovery and cutover](deploy/README.md#asset-scan-execution-recovery-0121).

Migration 0122 records upload ownership before storage writes and attaches it atomically with the asset, scan binding and audit. Failed or interrupted uploads have durable verified cleanup, late-write rechecks, hold protection, account deletion/export integration and independent backlog alerts. Stop and drain old API/worker instances before coordinating the migration, application and metrics checker; nonempty journals prevent downgrade. Client upload idempotency is added by 0123 below; unknown historical objects/backups and real storage/capacity acceptance remain open. The running database has not been upgraded. See [upload journal cutover](deploy/README.md#upload-write-journal-cutover-0122).

Migration 0123 adds account-scoped upload commands with immutable attempt/result evidence. Both upload endpoints require a retry key: matching requests recover the same asset/version, while changed content conflicts. The shared browser client hashes actual bytes and retains uncertain requests across same-tab refresh; login changes cannot mix in-flight operations. API, worker, migration, frontend and generated contract must ship together after draining old processes. Isolated fault, HTTP and browser tests do not establish production readiness. See [upload command cutover](deploy/README.md#upload-command-cutover-0123).

Migration 0115 connects multi-file submission, complete approval/public eligibility and the existing ZIP purchase path. Database checks reject incomplete purchase assets and identity rebinding; connections declare the bundle protocol and old workers cannot claim/renew running jobs. Isolated service tests cover real draft-to-checkout, signed simulated payments/refunds, whole/member reads and physical copy cleanup; browser tests cover publication and buyer presentation. Stop old API traffic and drain workers before migration; running jobs block migration, and accepted/publication evidence blocks rollback. No running database upgrade or real merchant acceptance has occurred. See [cutover requirements](deploy/README.md#bundle-publication-cutover-0115) and [current bundle scope](docs/resource-marketplace-bundles.md#10-多文件公开销售与部署协议0115).

Migration 0106 adds automatic read-only reconciliation for recorded unresolved Stripe product refunds. A separate worker pass runs at startup and every minute, enqueuing at most 100 candidates under a ten-second budget. It waits for refund dispatch to finish, starts after 15 minutes and backs off to daily reads. Queries reuse original-merchant verification and durable result handling; they never send a new refund. Failed/cancelled queries retain evidence and require operator recovery. Shared history and owner exports distinguish automatic from operator checks. API, worker, migration and frontend must ship together; the running development database has not been migrated. Waffo, full financial reconciliation and production acceptance remain incomplete; see [scope and evidence](docs/resource-marketplace-flows.md#76-未决退款自动只读核对0106).

Migration 0107 adds one-shot Waffo product-refund dispatch permits and a bound connector response contract. The reservation commits before sending; lost/mismatched responses or process interruption stop repeat dispatch and surface in shared operator review/history. Old unresolved operations receive no fabricated permits. Stop/drain old API and workers before coordinating the migration, API, worker and connector release; old workers cannot safely coexist. Waffo queries/recovery and actual-provider acceptance remain incomplete. See [scope and cutover](docs/resource-marketplace-flows.md#77-waffo-退款响应绑定与一次性派发0107).

Migration 0108 binds Waffo product webhooks to the immutable original store, buyer, order and transaction, independent of new-sales selection. The connector attests to the verified raw-body hash and deployment environment; event/job/binding commit together and workers recheck before applying money or rights. Original matched callbacks continue after sales are disabled or stores change. Missing historical proof requires authenticated same-body re-verification and guarded recovery; never infer it from current settings. Coordinate API, worker, migration and webhook-contract.mjs after draining old processes. Real Waffo acceptance and query recovery remain incomplete; see [scope and evidence](docs/resource-marketplace-flows.md#513-waffo-原交易回调与配置切换0108).

Stripe product callbacks now continue after new Stripe sales are disabled or another sales provider is selected, provided the original verifier/version/environment and global processing remain available. Ingress and workers share payment identity checks; mismatched queued evidence cannot grant rights. Platform financial events reject Connect/organization account contexts; connected-account status updates retain their own identity checks. This does not recover historical merchant/signing credentials or replace live-provider acceptance. No new migration is required for this Stripe change; deploy API and worker together. See [scope and evidence](docs/resource-marketplace-flows.md#514-stripe-新销售开关与既有商品回调分离).

Stripe runtime requests reject same-origin and cross-origin redirects without mutating shared caller clients. Redirect responses cannot replay financial POSTs or supply substitute identity/query evidence; pending product refunds retain rights and require reconciliation. See [transport boundary and regression evidence](docs/resource-marketplace-flows.md#515-stripe-出站请求的重定向边界).

Delivery-file recovery (migration 0095) is available to media operators at `/admin/deliveries`: it verifies the originally accepted bytes, records a new immutable location and supports interrupted attempts without changing purchase rights. Shared publication guards protect backups, and order cleanup owns all replacement locations. Recovery uses the frozen original, an eligible private asset backup (ordinary uploads remain limited to 10 MiB), or a dedicated exact-byte backup upload up to 100 MiB (migration 0096). Direct upload reserves a repair first, verifies bytes before taking business locks, rechecks current permission/retention and supports retrying the same target. API, worker, frontend and nginx configuration must be deployed together; historical orders without delivery snapshots are not supported. See [direct upload and deployment limits](docs/resource-marketplace-flows.md#69-原始备份直传恢复0096). See [the recovery scope and validation](docs/resource-marketplace-flows.md#68-损坏丢失交付文件的修复0095).

Data-rights request projections and controlled export generation/expiry recovery additionally require migration 0099; operators use the shared recovery queue under `admin:data-rights`. Owner cancellation and ready-file expiry remain authoritative.

Account data exports depend on migration 0098: complete packages are stored in parts of at most 5 MiB and downloaded as one verified JSON file, with seven-day body expiry. Large exports are no longer rejected solely for exceeding 5 MiB; resource and recovery limits remain documented in [the lifecycle audit](docs/resource-marketplace-flows.md#87-完整账户导出的分块存储与下载0098).

Export generation and download now enforce `DATA_EXPORT_MAX_BYTES` (default 512 MiB); each streamed database record is bounded by `DATA_EXPORT_MAX_ROW_BYTES` (default 8 MiB). Nested support/delivery/refund histories stream separately in the same snapshot. `DATA_EXPORT_TEMP_DIR` selects an existing private writable directory, and `DATA_EXPORT_MIN_FREE_BYTES` defaults to a 64 MiB safety floor. Capacity failures never publish truncated packages and retain the original request for guarded recovery. The production Compose template mounts a dedicated 1280 MiB tmpfs per API/worker container: tmpfs consumes RAM, so size host/container memory accordingly and review mount capacity when increasing limits. Free-space checks are not cross-process reservations; database and multi-instance capacity acceptance remain incomplete. See [resource controls and recovery](docs/resource-marketplace-flows.md#812-导出资源预算与嵌套历史流式读取).

Migration 0100 adds guarded recovery for failed account-deletion jobs at `/admin?tab=dataRights`, using the shared recovery queue and request-job service. Preparation and irreversible cleanup remain distinct; recovery preserves the original grace period, legal holds, required marketplace delivery files, and immutable failure/retry evidence. Legal-hold creation/release and deletion phases now serialize by account; releasing a hold also resolves older missing or cancelled request links. Automatic rescheduling after natural hold expiry is covered below; missing-job repair, alerting, and production storage acceptance remain incomplete. See [account deletion recovery](docs/resource-marketplace-flows.md#89-账户删除失败恢复与法律保留串行化0100).

Migration 0101 adds legal-hold expiry indexes. The worker scans immediately on startup and each minute (100 records / 10 seconds per pass), atomically persisting expiry and rescheduling the current blocked deletion under the original deadlines. Busy or failing subjects remain eligible for later rotating scans; replacement holds and valid purchased deliveries remain protected. This is account-deletion rescheduling, not a general cleanup scheduler. See [expiry scope and tests](docs/resource-marketplace-flows.md#810-法律保留自然到期与自动重调度0101).

Migration 0102 adds durable cleanup checks and immutable dispatch evidence for ended legal holds. Worker maintenance scans released/expired holds, evaluates up to 20 associated orders per batch, then advances account cleanup in separate per-account transactions. Each stage persists its cursor with dispatched jobs; restart continues partial work without rewriting original failures. Existing cleanup handlers recheck current holds/entitlements and enforce storage deletion. The scheduler records eligibility review, not physical completion; subsequent storage failures still use operator recovery. Account exports include owner-scoped check/dispatch evidence. API, worker and migration must ship together; see [ended-hold cleanup scope](docs/resource-marketplace-flows.md#811-保留结束后的媒体清理复核与派发0102).

Migration 0103 reconciles missing cleanup for independently snapshotted product orders whose payment and order evidence is resolved. It atomically records a new job, immutable predecessor link and audit, while preserving failed/cancelled jobs for separate handling. Workers recheck rights, holds and funds; operator retries inherit the same check. Buyer exports include their own dispatch evidence. API, worker, migration and shared recovery reason translations must ship together. General account-media reconciliation and production acceptance remain incomplete; see [scope and verification](docs/resource-marketplace-flows.md#813-独立订单副本遗漏清理与资金复核0103).

Migration 0104 restores missing execution for original owner-requested account deletions with elapsed deadlines, consistent preparation evidence and no active hold. Jobs, immutable predecessor links and audit commit together. Every later execution/recovery of a reconciled request rechecks the same evidence; failed or cancelled jobs are not automatically reset. Active-owner exports include their own dispatch linkage; deleted accounts remain unable to export. API, worker and migration must ship together; see [deletion reconciliation scope](docs/resource-marketplace-flows.md#815-注销请求执行遗漏与阶段证据复核0104).

Migration 0105 adds missing cleanup for managed original media after a verifiable completed account deletion. It reuses current rights/hold retention, preserves failed/cancelled jobs, and records immutable dispatch lineage. Workers verify the primary location is absent after Delete before saving a per-location receipt and audit; already-absent files remain distinguishable from observed deletions. Execution and operator recovery inherit the original completion-evidence guard, and exports expose only owner-scoped records without storage locators. API, worker, migration and the shared recovery-reason translations must ship together. The running development database has not been migrated. Provider versions/backups, unknown storage objects, out-of-band restores, inconsistent historical consent, capacity and alerting remain outside this checkpoint. See [original-media scope and evidence](docs/resource-marketplace-flows.md#817-原始媒体遗漏补发与逐文件清理证明0105).

## Requirements

- Go 1.26.8 or newer
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

Periodic marketplace maintenance health requires migration `0109_maintenance_health` plus the matching API, worker and metrics-check script. Never-run, failed and stale scans now fail the check; a fresh deployment remains unready for this check until the worker records successful passes. See [maintenance health and rollout](deploy/OBSERVABILITY.md#periodic-maintenance-health-0109).

Running-job leases now have expiry/invalid-evidence metrics and bounded recovery even at full worker concurrency. Expired owners cannot renew or finalize tasks; heartbeat and terminal-write deadlines prevent database lock waits from indefinitely occupying an execution slot. Migration `0110_running_job_leases` adds the expiry index but does not repair historical evidence. Recovery can re-execute work, so business idempotency and payment/storage result verification remain necessary. See [lease monitoring and rollout](deploy/OBSERVABILITY.md#running-job-leases-0110).

Development starts with migrations only. Demo login and actor switching have been removed. Put local configuration in the gitignored `.env.local` (owner-only permissions); `make dev` loads it for the API and Worker:

```bash
make dev
```

Individual processes are available through `make db-up`, `make migrate`, `make api`, `make worker`, and `make web`; inject environment variables when starting these directly. `internal/testfixtures/cmd/seed` is restricted to `APP_ENV=test` and an isolated, non-public database schema. Tests authenticate those fixtures through password login.

For an old development database already processed by migration 0070, `APP_ENV=development DATABASE_URL=... ./scripts/purge-retired-demo.sh --apply` physically removes the four retired shared identities and their known historical data. This explicit maintenance command runs in one transaction, preserves unrelated users/settings, refuses unexpected dependencies, and rebaselines the remaining audit chain. It is never run automatically or in production. The September 18 local cleanup and verification results are recorded in `docs/PROGRESS.md`.

`npm --prefix web run test:e2e -- identity-full-flow.spec.ts password-policy.spec.ts --project=chromium` checks registration, email codes, password login/reset and session revocation with an isolated mailbox. A manual real-SMTP run can use `web/playwright.live-auth.config.ts` only with an explicitly supplied `HCAI_LIVE_AUTH_EMAIL` and private `HCAI_LIVE_AUTH_INPUT_DIR`. It uses the running development app and waits for recipient-provided `registration-code.txt`, `login-code.txt` and `reset-link.txt` files. Use a new temporary test address; clean up its account, mail evidence and private input files after the run. Live tracing/screenshots/video are disabled.

Run the isolated real-process worker recovery drill with PostgreSQL client tools and `jq` available:

```bash
make recovery-drill
```

The drill uses a temporary schema, media directory, and loopback port (`DRILL_HTTP_PORT`, default `18081`). It stops only the worker process it creates, submits a generation while that worker is offline, restarts processing, verifies one Asset and one Local Test charge plus idempotent replay and Admin diagnostics, then removes all temporary state.

Run the isolated Stripe payment-provider drill with PostgreSQL client tools, `jq`, `curl`, `lsof`, and `openssl` available:

```bash
make payment-drill
```

This drill builds real API and worker binaries plus a loopback-only Stripe fixture, applies migrations and isolated test fixtures in a temporary PostgreSQL schema, and enables Stripe test mode only inside the child processes. It proves one hosted product Checkout call, signed payment and refund Webhooks, durable worker processing, entitlement grant then revocation, Checkout/Webhook/refund idempotency, one Connect Express account, two single-use Account Link renewals, signed `account.updated` synchronization to a verified payout destination, and Admin payment evidence. `PAYMENT_DRILL_HTTP_PORT` and `PAYMENT_DRILL_FIXTURE_PORT` default to `18082` and `18083`; the script rejects occupied ports and removes every process, file, and schema it creates.

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
| `TRUSTED_PROXY_CIDRS` | Comma-separated non-zero CIDR prefixes for actual deployment proxies. Network evidence walks `X-Forwarded-For` right-to-left from the TCP peer, stopping at the first untrusted hop; every trusted proxy must append its observed peer or replace the header with a verified chain. Empty means forwarded headers are ignored. |
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
| `EMAIL_DELIVERY_MODE` | `smtp` sends through implicit TLS with retries; `local_file` is test/development only; `disabled` prevents delivery. |
| `SMTP_HOST`, `SMTP_PORT` | SMTP hostname and implicit TLS port (default `465`), with certificate verification. |
| `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` | SMTP credentials and bare sender address, injected through the environment. Never expose these to the frontend. |
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
| `WAFFO_CHECKOUT_LOOKUP_QUERY` | Optional schema-reviewed read-only GraphQL query for product Checkout recovery. Keep empty until the real merchant schema, query authorization and end-to-end matching semantics are approved; an empty value fails closed. |
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

Production startup fails when PostgreSQL does not use `sslmode=verify-full`, `WEB_ORIGIN` is not a public path-free HTTPS origin, placeholders remain, S3 storage or the authenticated HTTP scanner is not configured, the local provider is enabled, secure cookies are disabled, loopback Webhook targets are allowed, either 32-byte encryption key is absent or the two keys are reused, identity email delivery uses `local_file`, SMTP delivery is selected with incomplete configuration, or the OpenAI endpoint is not the official HTTPS API. Stripe production enablement additionally requires approved live mode, matching live credentials, and the pinned official endpoint. OpenAI registration additionally fails without both explicit paid-call approval and a credential. Forwarded client headers are ignored unless the direct TCP peer is inside `TRUSTED_PROXY_CIDRS`; the API walks the forwarding chain right-to-left until the first untrusted hop. Each trusted deployment proxy must append its observed peer or replace the header with a verified chain; never configure ordinary client networks as trusted proxies. Network evidence is stored only as a one-way minimized hash, and retention, notice, and legal-basis acceptance remain production responsibilities. No secrets, paid-provider credentials, real payment details, or personal production data belong in the repository.

## Database and seed

Migrations are embedded from `internal/platform/database/migrations` and recorded in `schema_migrations`. Running `make migrate` more than once is safe.

`make test-fixtures` requires `APP_ENV=test` and an isolated non-public schema. It creates deterministic test users and workflow fixtures, uses normal password login, and never issues fixed shared sessions. `make dev` does not seed data.

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

The Community feed applies active-author, published Post/Work and clean-Asset visibility, with 30-minute viewer/filter-bound ordering snapshots for latest and discussed pagination. Each page rechecks visibility and returns full result/category counts. Authenticated notification-delivery evidence applies account ownership before its cursor, includes durable delivered/suppressed attempt state, and never exposes job payloads or worker internals. Both pages default to 20, accept 1-50, reject modified cursors, and append without duplicate IDs.

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

For external mail, use `EMAIL_DELIVERY_MODE=smtp`, configure the SMTP variables, and generate `EMAIL_ACTION_ENCRYPTION_KEY_B64` with `openssl rand -base64 32`. Keep that key stable across the API and Worker and across restarts. SMTP is used for registration/login codes, email verification, and password resets. Failed deliveries use the durable queue's bounded retries; SMTP acceptance is not proof of inbox delivery. Bounce/complaint ingestion is not implemented. `go run ./cmd/mailcheck` checks TLS and authentication; `go run ./cmd/mailcheck -send-test` additionally sends one test email to `SMTP_FROM`. Both require the configured environment. The Admin recovery queue exposes only masked recipient and bounded attempt evidence.

The owner Account history defaults to five recent actions and exposes an opaque stable cursor for older evidence. Pages are capped at 50, enforce account ownership before cursor conditions, and never return message bodies, action tokens, ciphertext, or complete recipient addresses.

Owner and Admin data-rights request histories are independently cursor-paginated with pages capped at 50. Admin legal holds use a separate active-priority cursor with a frozen evaluation timestamp, so a hold expiring during traversal does not duplicate or skip historical evidence. Account and Admin pages append each resource independently and reload synchronized first pages after a hold change.

## Testing

Run Go unit and PostgreSQL integration tests plus frontend unit tests:

```bash
make test
```

`make test` requires the PostgreSQL integration database and fails if it is unavailable. Set `TEST_DATABASE_URL` to an isolated database. Both it and `make test-integration` run Go tests uncached (`-count=1`); the integration target also requires `TEST_DATABASE_URL` explicitly. Both use a finite `GO_TEST_TIMEOUT` (default `60m` per Go package). Payment integration fixtures each apply the full migration history; the complete package has exceeded the previous 30-minute cap. CI reserves 90 minutes for setup, tests and static checks. A timeout is a failed run, not a pass; rerunning the remaining tests provides separate coverage evidence, not an uninterrupted full-suite result. Override the package budget explicitly with, for example, `make test GO_TEST_TIMEOUT=75m`, and keep the CI job budget larger if changing it there.

`make security` requires network access to the Go vulnerability database/module registry and the official npm audit service. It uses pinned govulncheck v1.7.0, rejects vulnerabilities in imported Go packages, and audits production dependencies from both npm lockfiles. It does not need a database or payment credentials. A successful check only covers known dependency advisories, not application logic or complete production acceptance.

`make proxy-test` validates the production Nginx configuration and Compose web mounts in disposable containers: non-root/read-only startup, 10 MiB asset/version uploads, chunked transfer and route-specific body limits. Prepare the web and Node images as described in [deployment verification](deploy/README.md); the test never pulls images or starts the actual API/database. CI builds its own web image and runs this check independently.

Run static checks and the production build:

```bash
make lint
make build
```

Run all browser workflows. Playwright starts an isolated stack on ports `15173` and `18080`, seeds a temporary PostgreSQL schema, and removes that schema and its generated media when the run ends:

```bash
make e2e
```

Run the separate resource-marketplace payment browser journey:

```bash
make e2e-payments
```

It uses real application APIs and workers for seller upload, single/multi-file publication, review, checkout, public previews, purchased playback, package/member download and confirmed refund. PNG, JPEG, H.264 MP4, WAV, MP3 and UTF-8 text use procedurally authored fixtures; package and original bytes are verified separately from public previews. Only the external Stripe service and hosted checkout are simulated: a loopback fixture on port `18084` (override `E2E_FIXTURE_PORT`) and test-signed webhooks. It never contacts the real checkout or uses live payment credentials. The payment suite also includes shared text-preview limits and timeout recovery using controlled response failures. The ordinary suite excludes `*.payment.spec.ts`. Both suites share `hcai_e2e` and must run sequentially against a given database; CI runs payment acceptance in a separate job/database. Isolated API, worker and fixture logs are retained under `web/test-results/service-logs`. This is not real merchant acceptance or validation of all codecs/browser engines.

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
- Authentication requires a personal account. Legacy shared identities are anonymized with status `deleted` by migration 0070. Sessions, linked login methods, and email tokens are removed or invalidated; content is retired and assets blocked. Foreign-key placeholders and immutable transaction/audit evidence remain.
- Google and GitHub remain fail closed until credentials and staging verification are complete.
- Notification producers atomically queue one owner-scoped row and one durable job. The worker applies the latest preference, records immutable delivered/suppressed evidence, and exposes only a safe owner projection without payload, lease, worker, or raw-error data.
- Deep links are internal and allowlisted, read state is independent from opening the linked workflow, and task, order, refund, generation, Community, governance, Asset, support, and data-rights producers deduplicate on replay.
- Community comments, reactions, bookmarks, and follows are persisted and scoped to the authenticated actor.
- Reports and appeals retain ordered governance evidence. Admin decisions require permission, reason, confirmation, notifications, and audit; upheld appeals restore the prior content state atomically.
- Uploaded Assets default to pending and fail closed until scanning completes. Review/rejected content cannot be read or published; Admin Media changes require permission, reason, confirmation, owner notification, and audit evidence.
- Asset versions store new bytes as ordered family members and pass through independent scans; prior versions and predecessor links remain inspectable, while purchased originals cannot be versioned.
- Publishing drafts are private PostgreSQL records with optimistic versions. Refresh and cross-session recovery are supported, and publish revalidates ownership, clean scan state, disclosure, and purchased-original restrictions.
- Data export requires a session issued within 15 minutes and exact handle confirmation. The private JSON package is byte-bounded, checksum-addressed, owner-only, and purged after seven days by a durable retention job.
- The same repeatable-read export includes buyer Marketplace contracts, entitlements, delivery/payment/refund/recovery history and separately scoped seller product/sales summaries. It excludes storage locations, checkout/return URLs, raw Provider payloads and private operator notes. Migration 0098 supports multipart storage with a single verified JSON download, and 0099 adds guarded recovery for failed generation and expiry jobs. Natural legal-hold expiry rescheduling for blocked account deletion is implemented in the worker (0101). Multi-instance resource acceptance, general cleanup rescheduling, missing-job reconciliation, and the complete transaction-retention policy remain incomplete. See the [Marketplace export scope](docs/resource-marketplace-flows.md#85-个人资源市场交易证据导出).
- Account deletion has a 30-day cancellation window. Local primary processing revokes access, deletes owned local media, anonymizes identity/content, redacts Support free text, retains bounded transaction/audit/safety/Support facts, and creates an immutable per-domain receipt. Controlled legal holds block and resume deletion with hashed authority evidence.
- Production backup expiry and external Provider deletion are not claimed by local receipts; those steps remain fail-closed until verified production integrations and legal acceptance exist.
- Support cases are requester-owned and private. Copyright intake requires a stable public platform target and bounded claimant statement, rejects common high-risk identifiers, and does not claim to determine legal ownership.
- The global product shell exposes Terms, Privacy, Cookies, Acceptable Use, AI Disclosure, Licensing, Refunds, Copyright, and private Support to anonymous and authenticated users. It labels the bundled copy as Local Test product terms and keeps production legal acceptance explicit.
- Support messages/events are append-only during normal operation. The data-rights worker can write only fixed redaction markers while preserving every structural evidence field; Admin replies and state decisions still require `admin:support`, a specific reason, explicit confirmation, optimistic version agreement, requester notification, and audit evidence.
- Asset version events are append-only during normal operation. Owner exports include the event history; account deletion clears version notes and permits only the fixed event-reason marker through the constrained maintenance boundary.
- Task disputes, verified historical internal reversals, Community reports, uploaded-media rejection, and registration account-link thresholds atomically create idempotent risk signals. Account-link evidence retains aggregate counts and exact rule attribution but no raw IP, network hash, device fingerprint, or linked-account list; ordinary login does not produce it. Review requires `admin:risk`, a reason, confirmation, and the expected version; terminal decisions cannot be overwritten and every decision appends risk plus Admin audit evidence.
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
- Historical internal refunds require original balanced payment evidence, enforce the accepted window, reverse the original payee, revoke the entitlement and retain audit evidence. Unverified records require reconciliation; no new local purchase is available. Purchased-asset seller identity comes from the original contract/payment evidence, not the current catalog owner.

Legal copy, payment terms, tax availability, production privacy review, retention policy, and copyright operations require dedicated acceptance before a production release.
