# Production observability baseline

The API exposes `GET /metrics` for an internal Prometheus-compatible scraper. The endpoint is intentionally not proxied by the public Web container. Place the scraper on the private Compose/network side or expose it only through an authenticated operations proxy.

The series are deliberately low-cardinality and contain no request IDs, URL parameters, user IDs, prompts, email addresses, Provider response data, or credentials:

- `hcai_database_ready`
- `hcai_audit_chain_valid`
- `hcai_http_requests_total` by status class only
- `hcai_http_request_duration_seconds_*` and response bytes
- `hcai_jobs_total` by durable status
- `hcai_jobs_oldest_queued_age_seconds`, counting only work whose `available_at` has arrived
- `hcai_jobs_expired_leases`, `hcai_jobs_oldest_expired_lease_age_seconds` and `hcai_jobs_invalid_leases` for running-job lease recovery
- `hcai_job_attempts_total` by status for the last 24 hours
- `hcai_recovery_jobs_failed{kind=...}` for five fixed recovery categories, including failures older than 24 hours; emitted as zero when empty
- `hcai_product_payment_backlog{kind=...,mode=...}` and `hcai_product_payment_oldest_age_seconds{kind=...,mode=...}` for transaction backlog counts and ages
- `hcai_product_payment_problems{kind=...,mode=...}` for product event failures, stopped refund checks and evidence anomalies
- `hcai_maintenance_*{kind=...}` for durable periodic-scan health across worker processes

Run the bounded smoke check from an operations host:

```bash
METRICS_URL=http://api:8080/metrics make metrics-check
```

The check exits non-zero when PostgreSQL is unavailable, the audit chain is invalid, runnable queued work exceeds `ALERT_MAX_QUEUED_AGE_SECONDS` (default 900 seconds), failed attempts exceed `ALERT_MAX_FAILED_ATTEMPTS_24H` (default 0), or the sum of the five recovery-failure categories exceeds `ALERT_MAX_RECOVERY_FAILURES` (default 0). Product financial checks add the thresholds described below. Future scheduled work is excluded until its `available_at` time arrives. Missing, duplicate or invalid required samples also fail the check; only the existing optional daily failed-attempt series defaults to zero when absent. Update the API and check script together: an older API without recovery/financial metrics will deliberately fail this check.

## HTTP proxy logs and temporary credentials

The Web proxy now emits one JSON `kind=http_access` record per completed request. Fields are `requestId`, `method`, `path`, `status`, `bytes`, `durationSeconds`, `upstreamStatus` and `upstreamSeconds`; field values are strings. `path` is the original path before SPA fallback, without the query string. Query values, Referer, cookies, Authorization, user agent and client address are not part of this projection. Update log parsers from the old combined format before rollout.

The proxy creates a fresh 32-hex `requestId` and forwards it as `X-Request-ID` to every upstream route, replacing any client-supplied value. The API's existing middleware uses that ID for its structured request result and response header. Correlate upstream request handling by this ID; proxy-only rejection has no API counterpart. Use status/timing and upstream status/timing to detect 4xx/5xx, aborted requests and failed upstream calls.

Nginx native per-request diagnostics cannot be customized with `log_format` and may include raw URLs/Referer. They are therefore suppressed within the HTTP server; startup/process diagnostics remain on the main stderr error log. Do not re-enable raw request diagnostics on live traffic as a debugging shortcut. Investigate failures using the safe result record, API/worker logs, readiness and internal metrics. A collector or external TLS proxy needs its own equivalent redaction policy; these changes only govern this checked-in Web proxy.

Static/SPA responses send `Referrer-Policy: no-referrer`, so opening an email-action link does not send its token-bearing URL as a Referer on subsequent requests. `make proxy-test` uses synthetic credentials and checks both output streams for leakage on 200, 413 and 502, verifies the safe result records remain, and checks request-ID propagation. No real email-action token is used. This configuration is not yet deployed and does not retroactively redact retained historical logs.

## Persistent recovery failures

| Fixed `kind` | Operation | Investigation entry |
| --- | --- | --- |
| `media_account` | Original account media cleanup | Operations → data rights → media cleanup |
| `media_product` | Purchased delivery copy cleanup | Operations → data rights → media cleanup |
| `export_export` | Account data export | Operations → data rights → export jobs |
| `export_expiry` | Expired export cleanup | Operations → data rights → export jobs |
| `account_deletion` | Account deletion preparation or cleanup | Operations → data rights → deletion jobs |

The aggregate reuses the operations queue projections. It counts failed jobs with no explicit recovery successor, excluding already removed product deliveries and explicitly closed/purged requests. A failed successor is counted in its own right; its preserved predecessor is not counted again. Missing subjects, legal holds and other reasons that prohibit immediate retry are still investigation signals, not successful completion. A queued replacement clears this *failure* signal but can still trigger the runnable-queue-age check. Counts represent failed jobs, not distinct users, orders or payment obligations. Independently created jobs without recovery lineage do not erase old failed evidence.

Investigate the matching operations queue, check its current eligibility and underlying storage/service failure, then use the authorized recovery action. Do not delete evidence or manually mark jobs succeeded to silence an alert. Check the replacement's terminal result; metric zero alone does not prove deletion, delivery or refund completion. If cleanup is blocked by a legal hold or retained delivery, follow the retention workflow rather than bypassing it.

Example Prometheus expression (separate from endpoint availability alerts):

```promql
sum(hcai_recovery_jobs_failed) > 0
```

Configure an external scheduler or Prometheus alert rule to page an operator; this repository does not provide paging or centralized retention. Scrape failures/timeouts must alert separately (`up == 0` and missing-target monitoring). The recovery-job counts above do not cover financial transactions, running leases or periodic worker scans; those have separate metrics below. Cancelled/missing cleanup jobs and full storage reconciliation remain outside these counts. `/metrics` retains its two-second deadline and returns 503 if a projection fails; production-scale query cost and paging acceptance remain unverified. The recovery projections require existing migrations through 0105; the financial aggregates also use the refund candidate view from 0106. Those aggregates introduced no migration; maintenance telemetry and the running-lease index require 0109 and 0110 respectively.

Media recovery failure counts use the actual failed job, replacement linkage and delivery snapshot removal state. They intentionally do not expand the operations queue's complete financial/retention/retry-permission projection. Held deliveries, active entitlements and unresolved orders still count as failed cleanup work; only actual removal or a linked replacement excludes that failed leaf. This keeps failure visibility separate from permission to retry. The change addressed expensive metrics queries observed during HTTP regression; the two-second deadline remains unchanged. Local small-fixture timings and final HTTP regression are recorded in [11.129](../docs/resource-marketplace-flows.md#11129-运行镜像架构代理版本与指标查询); they do not establish production capacity.

Cleanup queue reads and the first eligible recovery use transaction-local `jit=off`: on the isolated policy fixture, JIT compilation accounted for about 1.17 seconds of a 1.20-second execution, whereas the same query without JIT executed in about 5 ms plus 44 ms planning. Duplicate/active recovery requests are rejected under the existing shared locks before expanding the full policy. These adjustments preserve eligibility and worker rechecks, restore pooled connection settings after commit/rollback, and do not change global PostgreSQL configuration. Capacity and lock-wait acceptance still require production-scale evidence; see [11.130](../docs/resource-marketplace-flows.md#11130-清理重试并发查询与完整回归续测).

## Running job leases (0110)

Lease monitoring is fleet-wide and independent of `ALERT_PAYMENT_MODE`. It exposes three unlabeled gauges, including zero values:

| Metric | Meaning |
| --- | --- |
| `hcai_jobs_expired_leases` | Running jobs with finite deadlines at or before database time |
| `hcai_jobs_oldest_expired_lease_age_seconds` | Longest elapsed time since such a deadline, zero when none exist |
| `hcai_jobs_invalid_leases` | Running jobs with missing/blank owner, missing token, missing/nonfinite deadline, or no matching running attempt by job, token and attempt number |

Malformed finite-expiry jobs can appear in both expired and invalid counts; do not sum them as distinct jobs. A normal long-running handler with valid heartbeats is not expired. These gauges do not establish handler deadlines, per-replica health or exactly-once business effects.

The check script fails for any invalid lease or an expired age greater than `ALERT_MAX_EXPIRED_LEASE_AGE_SECONDS` (default 60 seconds, equality allowed). Missing, duplicate, malformed or inconsistent zero-count/nonzero-age samples fail rather than appearing healthy. Deploy the API and script together. A scrape/projection failure remains a separate 503/unavailability signal.

The worker recovers at most 100 expired jobs per poll, before checking execution capacity. Recovery and claim each have a five-second timeout and use row locking with `SKIP LOCKED`. Recovery requires a finite expired deadline, nonblank owner, token and matching running attempt. It preserves the old attempt as `lease_expired`, then queues the job or marks it failed if its attempt budget is exhausted. Invalid evidence is left intact for investigation. Renewal and terminal writes check database wall time after acquiring the job lock. The worker verifies its lease before entering a handler; each heartbeat renewal is bounded by one-third of the lease and cancels the handler context on failure. Success/failure finalization has the same one-third-lease budget, so a blocked terminal write releases the execution slot instead of starving other work. If finalization times out, the original durable job/attempt remains subject to recovery; it is not marked successfully completed by the timeout handler.

When an alert fires, inspect worker/database availability, stable failure codes, lock waits and the retained job/attempt evidence. For a valid expired job, verify automatic recovery and then its actual business result. For a terminal failed job, use the corresponding authorized recovery workflow. For malformed evidence, investigate the original failure or migration history; do not invent an attempt, clear a token, or mark a job succeeded to silence the alert. Recovery may execute the job again, so provider idempotency, dispatch evidence and verified storage results remain required. Cancellation cannot stop a handler that ignores its context.

Example external rules:

```promql
hcai_jobs_oldest_expired_lease_age_seconds > 60
hcai_jobs_invalid_leases > 0
```

Migration `0110_running_job_leases` adds a partial `(lease_expires_at,id)` index for running jobs. It does not rewrite existing jobs. It uses ordinary `CREATE INDEX`, so schedule for the table size and write-lock impact; it is not an online concurrent-index migration. Down drops only this index, preserving execution evidence. Coordinate worker/API/script releases and evaluate rollback semantics: older workers do not enforce the new expiry checks. Local isolated-schema verification does not prove production capacity, paging delivery, or safety of all external side effects.

## Product payments, refunds and settlements

Financial aggregates use one database snapshot and fixed `mode="live"` / `mode="test"` labels. All combinations, including zeros, are emitted. There are no merchant, account, order, payment or event identifiers in labels. `ALERT_PAYMENT_MODE` defaults to `live`; `test` and `all` are explicit alternatives. This setting only filters the new financial checks: the older queue, daily failures and data-rights checks remain global.

| Backlog `kind` | Count and clock | Maximum-age variable (default seconds) |
| --- | --- | --- |
| `checkout_pending` | Payment intents still preparing/creating checkout, measured from original intent creation, including preparation before a provider request is stored | `ALERT_MAX_CHECKOUT_PENDING_AGE_SECONDS` (900) |
| `checkout_expired` | Open sessions past their expiry, measured from that expiry | `ALERT_MAX_CHECKOUT_EXPIRED_AGE_SECONDS` (900) |
| `refund_unresolved` | Individual refund attempts requested, pending or marked for reconciliation, measured from original `requested_at`, even if the parent payment says refunded | `ALERT_MAX_REFUND_UNRESOLVED_AGE_SECONDS` (86400) |
| `refund_check_due` | Eligible automatic Stripe checks past their due time, using `product_refund_reconciliation_candidates` and its existing backoff/eligibility; 0134 includes evidenced closed orders with unresolved original refunds/read gaps | `ALERT_MAX_REFUND_CHECK_DUE_AGE_SECONDS` (900) |
| `settlement_due` | Positive net settlement without transfer proof or dispatch reservation, due in `pending_hold`, `available` or `provider_unsupported`, with a paid payment and fulfilled order; clock is original `available_at` | `ALERT_MAX_SETTLEMENT_DUE_AGE_SECONDS` (900) |
| `settlement_unresolved` | Recovery-required state, positive recovery debt, or a reserved/pending dispatch without transfer proof; clock is original reservation, falling back to settlement creation | `ALERT_MAX_SETTLEMENT_UNRESOLVED_AGE_SECONDS` (900) |
| `seller_funding_unresolved` | Source transfers in `requested`, `processing` or `reconciliation_required`, including missing dispatches and unstarted stopped jobs; clock is immutable source `reserved_at` | `ALERT_MAX_SELLER_FUNDING_UNRESOLVED_AGE_SECONDS` (900) |
| `seller_funding_check_due` | Started sources eligible in `seller_payout_funding_check_candidates` whose cooldown has elapsed, with no active recovery; clock is the candidate's `due_at` | `ALERT_MAX_SELLER_FUNDING_CHECK_DUE_AGE_SECONDS` (900) |
| `seller_bank_unresolved` | Bank commands without a confirmed paid/failed/canceled result, including unqueued commands; clock is immutable command creation | `ALERT_MAX_SELLER_BANK_UNRESOLVED_AGE_SECONDS` (900) |
| `seller_bank_check_due` | Started bank commands with stopped original jobs and no active recovery whose next read is overdue; includes paid payouts due for return monitoring | `ALERT_MAX_SELLER_BANK_CHECK_DUE_AGE_SECONDS` (900) |
| `seller_reversal_unresolved` | Source-return commands without an immutable ledger closure; clock is command creation | `ALERT_MAX_SELLER_REVERSAL_UNRESOLVED_AGE_SECONDS` (900) |
| `seller_reversal_closure_due` | A complete source return was observed and is waiting for independent ledger closure; clock is accepted return result time | `ALERT_MAX_SELLER_REVERSAL_CLOSURE_DUE_AGE_SECONDS` (900) |

The script alerts when an age is **greater than** its limit, not equal to it. These are operational investigation thresholds, not refund deadlines, permission to move money, or proof of failure. Updating a payment/refund or scheduling a new check does not reset the unresolved refund clock. Future scheduled checks and unexpired sessions do not count as overdue. Stages overlap; do not sum them as distinct payments or financial amounts.

There are twelve backlog kinds (24 count and 24 age series across live/test). Settlement modes come from the immutable settlement snapshot; source funding modes come from the immutable source transfer, independently of mutable payment or parent request state. Missing-snapshot problems use the original payment mode. Normal future holding periods and refund-paused settlements do not enter `settlement_due`. A successful scan or replacement query does not reset an unresolved obligation's age. Restoring transfer proof does not clear recorded recovery debt. A source-return result is not a release: `seller_reversal_closure_due` remains until an authorized closure records the ledger outcome.

The problem series has twenty-two fixed kinds (44 series across live/test). Their sum in the selected modes must be no greater than `ALERT_MAX_PAYMENT_PROBLEMS` (default 0):

- `event_failed`: every still-failed event linked to a product payment, including older failures behind newer processed events. Payment operations now prioritizes a failed event so its replay action remains accessible. Re-enqueueing clears this failure state but is not completion; check the queued job and final event result.
- `checkout_check_missing`: due Stripe product sessions with original identity evidence and no historical check job, no prior automatic dispatch and no unresolved event. Migration 0124 supplies the shared eligibility view. The bounded worker scan schedules an authenticated read; this metric clears on scheduling, not payment completion. Ineligible identity/order gaps still require investigation.
- `checkout_check_stopped`: a still-open product checkout whose latest session-check job failed or was cancelled, with no queued/running replacement. This includes recovered paid sessions awaiting verification before the session expiry. Active recovery suppresses this signal without deleting the failed attempt; a settled payment is no longer counted. This signal does not detect a check that was never created.
- `refund_check_stopped`: the latest refund check failed, or its durable job failed/was cancelled. A new queued check supersedes the old failure without removing its evidence.
- `refund_read_unrecorded`: migration 0128 has a registered refund read without a saved outcome or valid later recovery, past its absolute read deadline plus five seconds. Counts distinct payments by live/test mode; normal reads inside their budget do not alert. Inspect finance query history and the existing due-query/failed-job status. Later complete authenticated reads must satisfy the time, original-payment and lost-lease conditions before recording recovery. Saved positive observations remain independently protected. Deploy the matching API, worker and alert checker with 0128.
- `refund_observation_unresolved`: a product payment has a saved authenticated refund observation without a matching local operation/payment/refund/amount/currency binding, or observed success has not been applied locally. Migration 0125 evaluates all saved observations; a newer failed or empty read cannot clear this signal. No local refund attempt is required to detect the problem. Correctly bound and applied evidence clears it; this is not authorization to fabricate a missing operation or force another refund.
- `refund_evidence_missing`: a refund-pending/failed product payment has no retained refund-attempt record. Investigate historical evidence; do not invent an operation or resend a refund.
- `checkout_evidence_missing`: an open checkout lacks a session ID or finite expiry, or lacks a URL without an immutable successful Stripe lookup or merchant-recovery Checkout proving that the same session is complete/expired. Terminal recovered sessions intentionally have no reusable payment link; an open-session lookup or proof for another session cannot excuse a missing URL. A valid terminal recovery does not excuse stopped verification: inspect `checkout_check_stopped` separately. Invalid infinite expiry is included in this category.
- `checkout_evidence_conflict` (0131): a Stripe product payment has differing saved paid Checkout observations across authenticated session lookup, merchant recovery or checkout query. Expiry timestamps are ignored. Counts distinct payments in live/test independently of payment or job status, including already-fulfilled orders. Shared review restricts normal refunds and retains delivery evidence; ordinary check retries do not resolve the discrepancy. Roll out API/worker and `metrics-alert-check.sh` together. Live conflicts alert by default; test conflicts alert only when opted in with `ALERT_PAYMENT_MODE=all`. Missing series is a deployment error, not a zero count. See [rules](../docs/resource-marketplace-flows.md#522-并发付款证据的一致性与持久冲突保护0131).
- `closed_checkout_paid` (0132): a cancelled/payment-failed Stripe product payment has saved paid Checkout evidence, or its preserved recovery event has not been processed. Scheduling alone does not clear it. Recovery first reads historical refunds; only qualified fresh complete evidence permits compensation. Once compensation has taken over, existing refund problems/backlogs apply. Unknown historical refunds remain retained, and normal retries do not adjudicate them. Live alerts default on; test-mode alerts require opt-in. Ship the producer/checker together; missing series is an error. See [rules](../docs/resource-marketplace-flows.md#523-已关闭历史订单的收款恢复与退款前核对0132).

  With 0134, pending original refunds on evidenced closed orders continue through the same automatic reconciliation scheduler and backoff. Before the next query is due, `refund_unresolved` and `closed_checkout_paid` still report the underlying obligation; due reads enter `refund_check_due`. Failed reads continue to require normal operator recovery and are not silently reset. No new metric kind is added. See [automatic historical follow-up](../docs/resource-marketplace-flows.md#713-历史关闭订单的未决退款自动跟进0134).

  With 0133, a complete authenticated history read may instead confirm a unique original full refund without issuing another refund command. The recovery marker still keeps `closed_checkout_paid` active until its original paid event is processed, even if the order already reads refunded. Successful confirmation and event handoff clear this recovery obligation; unrelated refund/evidence problems retain their own metrics and file holds. Partial/manual/multiple-success evidence or active old dispatch remains held. No new metric kind is introduced. See [historical refund confirmation](../docs/resource-marketplace-flows.md#524-已关闭历史订单的原退款确认0133).
- `settlement_missing`: no settlement matches a product payment and order with current fulfillment, ordinary refund request, any historical fulfillment event, or any retained entitlement (including revoked entitlements). Never-fulfilled compensation without such evidence is excluded; compensation metadata cannot hide retained fulfillment evidence. A confirmed refund does not erase a historical missing snapshot. This signal does not itself authorize creating financial records.
- `settlement_check_stopped`: an unresolved settlement without transfer proof has a latest failed/cancelled query job and no queued/running replacement. Scheduling a replacement clears this execution signal, not the unresolved obligation.
- `seller_funding_dispatch_missing`: an unfinished source transfer has no immutable funding dispatch. An unbound job with a matching payload does not resolve this gap. Do not fabricate a binding or resend funds to clear the alert.
- `seller_funding_admission_missing` (0162; deploy the updated alert script together with the metrics endpoint): a source transfer has no immutable admission consuming its approved review. Counts historical records even after source confirmation, in the original live/test mode. Unstarted legacy jobs cannot send funds; started legacy jobs remain eligible only for original-identity read recovery. Do not manufacture an approval, delete the source or change its status to clear this evidence gap. It requires a separately audited legacy reconciliation process, not automatic adoption.
- `seller_funding_stopped`: an unfinished source has a bound original job in `failed`, `cancelled` or `succeeded`, with no queued/running bound recovery. This includes original jobs stopped before their first start, which the scanner deliberately cannot recover. A queued/running original or bound recovery suppresses this execution signal, but retains the source backlog and its original age. Cooldown does not suppress the stopped-job signal.
- `seller_funding_review`: a source has any retained read marked `requires_review`. Counts sources, not read attempts, independently of source or parent status. Later retries, scheduling or observations do not remove historical conflicts. Authorized conflict adjudication is not implemented; do not delete read history to silence it.
- `seller_funding_read_unrecorded` (0158): a source has an unfinished registered read past its immutable `read_deadline` plus five seconds, or a historical unfinished read with no known deadline. Counts sources once in their original live/test mode, independently of parent payment or source status. Normal reads within the dispatch/persistence budget do not alert; scheduling another read does not clear the gap. A recorded terminal source result closes outstanding reads explicitly as `skipped`, retaining their IDs and clocks. This does not prove a missing remote response was received or finish a bank payout.
- `seller_bank_failed` (0166): a retained failed/canceled bank result needs financial reconciliation, including a return after paid. Failure before paid retains the reservation; failure after paid appends a return entry and restores the reservation. A stopped automatic scanner is intentional for these terminal failures; it does not settle the remaining financial obligation.
- `seller_bank_review` (0166): any retained bank read requires review. Later successful responses do not erase earlier contradictory or invalid evidence.
- `seller_bank_read_unrecorded` (0166): an unfinished registered bank read is past its durable deadline plus five seconds. A later accepted terminal result closes unfinished reads as `superseded`, preserving their IDs, clocks and the superseding read reference. Merely scheduling a retry does not clear the gap.
- `seller_bank_dispatch_stopped` (0166): the original job stopped before the first-send marker. Automatic read recovery does not resend these commands. Inspect eligibility, original authorization, creation window and runtime capability; an audited operator recovery path is still required.
- `seller_reversal_review`: a source-return read has contradictory, partial, invalid or otherwise review-required evidence. Retrying the provider read does not erase the retained observation.
- `seller_reversal_read_unrecorded`: a registered source-return read passed its durable deadline without a terminal persisted observation. Scheduling another read does not close the evidence gap.
- `seller_reversal_stopped`: the original source-return job stopped before its first-send marker and no active recovery remains. Automatic recovery is read-only and never resends the external request.
- `invalid_timestamp`: a counted backlog has a missing, infinite or future start time. Unfinished sources also count once per source if any retained dispatch, original/recovery job activity or read activity has an invalid clock, even when that clock prevents the candidate from becoming due. Normal future cooldown deadlines are excluded. Categories can overlap; this is an anomaly count, not distinct money at risk.

Source funding monitoring requires migrations through 0158 and matching API, worker and alert script releases. A registered read has a durable 20-second budget covering registration completion, subsequent connection/lock waits, authentication and network lookup, followed by at most five seconds to preserve its result. A paused worker cannot restart that window; a late provider success is retained as an error observation when it can still be saved, not accepted as source success. Earlier cancellation remains effective. Stop and drain older workers before migration because they do not enforce the durable deadline. New read registrations require `app.seller_funding_read_protocol=deadline-v1`, declared by `database.Open` on every connection; do not set it on older binaries to bypass the guard. Historical deadlines are not backfilled; unfinished historical reads alert immediately for investigation. Downgrade refuses once any bounded read exists. Never delete evidence to force rollback or silence an alert. Success of a source transfer clears its unfinished-source backlog only; bank reservations and parent reconciliation remain. Operator resolution remains outstanding; bank execution and recovery backend wiring is described below, with the later finance HTTP/UI scope documented in the current resource marketplace flow. The 900-second backlog defaults require operational tuning; validate paging and production query cost before rollout.

Bank execution monitoring (0166) requires matching migrations, API, worker and alert checker. `database.Open` declares `app.seller_bank_execution_protocol=journal-v1`; do not add this declaration to an old binary to bypass the guards. Deploy only after isolated migration and current-source regression verification. Downgrade refuses existing bank dispatch/read/result or debit/return evidence; never delete financial history to force it.

The `seller_bank_reconciliation` pass runs at startup and once per minute with an independent ten-second budget. Eligible unknown results have a five-minute cooldown, while confirmed paid results are checked after 24 hours to detect subsequent returns. Failed/canceled results stop automatic polling and retain a reconciliation obligation. Each read keeps a durable 20-second deadline plus at most five seconds to save evidence. First-send retries only query the original command; source `tr_` proof is never bank `po_` proof. New-write disablement does not remove the need to reconcile existing bank instructions using their original authenticated identity. Finance bank-command HTTP/UI and controlled unsent-job continuation now have isolated verification; consult the current resource marketplace flow for remaining production acceptance limits.

Bank labels use the immutable command's live/test identity. A scan heartbeat does not certify arrival at the bank, resolve an expired unstarted command, clear a returned balance, or authorize a second POST. Correlate bank results, ledger debit/return, parent request and original source records. Do not clear stopped/failed/review alerts by modifying job history, amounts, timestamps or retained observations. Validate real provider returns, paging delivery and long-term query cost before rollout.

Settlement monitoring requires migrations through 0145 and the matching API, worker and alert script. Inspect the retained settlement, original dispatch/batch and authenticated query evidence. The `product_settlement_reconciliation` heartbeat only proves the scanner completed a pass. Unknown results must follow original-transfer verification; do not resend transfers, delete evidence or clear recovery amounts to silence alerts. Authorized conflict adjudication and automatic reversal/debt recovery are not supplied by these metrics. Validate actual paging, production-scale query cost and provider behavior before rollout.

For financial alerts, open operations → payments, filter by environment and payment status, and inspect refund history or the selected failed event. Use supported authenticated query/replay actions and verify their terminal results. Waffo unresolved refund attempts are counted, but its active query recovery is still not implemented; a Waffo refund ticket is not proof of returned funds. No script action issues payments, retries jobs or changes evidence.

Example expressions (choose an appropriate `for` period in the external alert rule):

```promql
hcai_product_payment_oldest_age_seconds{kind="refund_unresolved",mode="live"} > 86400
sum(hcai_product_payment_problems{mode="live"}) > 0
```

These metrics cannot detect unknown remote transactions, unlinked/orphan events or missing refund operations on an otherwise apparently settled historical payment. They do not implement financial reconciliation, seller settlement, provider credential repair or notification delivery. Keep endpoint-unavailability alerts enabled and deploy the API and script together; missing required financial series deliberately fail the check. Production-scale performance, real provider results and an actual paging drill remain required acceptance steps.

## Periodic maintenance health (0109)

Migration `0109_maintenance_health` originally adds at most six operational rows; subsequent migrations expand the fixed kinds to thirteen through 0157. Telemetry does not enqueue work, change business evidence or synthesize a successful first run. Deploy the migrations, API, worker and check script together. An old worker will not populate the new telemetry; a new API against a missing table returns 503 from `/metrics`. On first deployment, all scans report no success until the new worker completes a pass. Allow an appropriate startup `for` period in the external alert rule; the command-line check deliberately reports this state immediately.

| Fixed `kind` | Pass |
| --- | --- |
| `legal_hold_expiry` | Expire eligible legal holds and resume eligible deletion |
| `legal_hold_cleanup` | Reevaluate cleanup after a released hold |
| `product_cleanup_reconciliation` | Dispatch missing eligible delivery-copy cleanup |
| `account_deletion_reconciliation` | Dispatch eligible deletion work with original request evidence |
| `original_media_cleanup_reconciliation` | Dispatch missing original-media cleanup with completion evidence |
| `product_refund_reconciliation` | Schedule eligible authenticated refund checks |
| `product_checkout_reconciliation` | Schedule eligible original Checkout checks |
| `product_settlement_reconciliation` | Schedule eligible authenticated original-transfer checks |
| `seller_funding_reconciliation` | Append read-only checks for started, unresolved seller funding whose original job has stopped; never resend source funds |
| `seller_bank_reconciliation` | Append read-only bank checks after original jobs stop, including later returns on paid payouts; never resend bank funds |
| `seller_reversal_reconciliation` | Append read-only checks for started source reversals after the original job stops, with a five-minute cooldown; never resend reversals or release local funds |
| `generation_output_cleanup` | Reconcile unbound generation writes and cleanup |
| `generation_execution_recovery` | Reconcile interrupted generation execution |
| `asset_scan_execution_recovery` | Reconcile interrupted asset scans |
| `upload_write_cleanup` | Reconcile unbound upload writes and cleanup |

Each pass retains its own ten-second business budget. A completed pass then gets a separate two-second telemetry write budget; telemetry failure logs only `maintenance_observation_failed` and the fixed kind, and does not stop subsequent scans. Shutdown cancellation does not manufacture a successful completion. A pass that returns nil after exhausting its deadline is recorded as failed. Scanners start immediately and then run on their minute tick; the five retention passes remain sequential, while refunds and the six later scans have independent loops.

The API exposes `hcai_maintenance_success_seen`, `hcai_maintenance_last_success_age_seconds`, `hcai_maintenance_last_pass_failed`, `hcai_maintenance_invalid_timestamps`, `hcai_maintenance_passes_total` and `hcai_maintenance_failures_total`. The script fails if any scan has never succeeded, its latest observation failed, its clock is ahead of database time, or its last success is older than `ALERT_MAX_MAINTENANCE_SUCCESS_AGE_SECONDS` (default 300 seconds). Equality is allowed. Repeated failures cannot refresh the last-success clock. Missing telemetry writes eventually produce a stale-success alert even when the last recorded outcome was healthy.

Seller funding recovery requires migration 0157 and matching API, worker and script. It scans at startup and every minute (100 candidates, 10-second budget), retaining a five-minute cooldown after the latest source/job/read activity. Only started unresolved transfers with stopped original jobs and no active check qualify. It appends immutable read-only jobs; never reset original attempts, clear reservations or resend transfers to resolve an alert. Correlate its heartbeat with `seller_funding_unresolved`, `seller_funding_check_due` and the funding problem kinds above; a healthy pass does not prove funds are resolved. Operator adjudication remains incomplete. Existing recovery bindings prevent migration rollback.

Counters are atomic across workers. Out-of-order writes preserve the newest observation's status and each outcome's own timestamp; a delayed earlier success cannot hide a later failure or borrow its timestamp. Database time is used rather than worker clocks. These are fleet-wide observations: one functioning worker may keep the scan fresh, and the metrics do not certify each individual replica. A successful pass with zero processed rows, skipped locks or an unavailable optional refund reader is not proof that all pending work is complete; correlate the financial backlog and recovery-job metrics.

Example alert conditions, in addition to unavailable/missing scrape targets:

```promql
hcai_maintenance_success_seen == 0
hcai_maintenance_last_pass_failed == 1
hcai_maintenance_last_success_age_seconds > 300
hcai_maintenance_invalid_timestamps == 1
```

Investigate the worker deployment, database access, stable error code and related backlog. Resume the worker through the deployment process and verify a fresh successful pass and the corresponding business outcome. Do not manually edit timestamps to silence alerts. The down migration discards only these operational counters and timestamps; coordinate application rollback first. It does not remove financial, hold, deletion or cleanup evidence. Running-lease monitoring is described separately above; request-observation purging, per-replica health, real paging and production capacity drills remain separate acceptance work.

## Generation output journal and cleanup (0116)

Migration `0116_generation_output_journal` adds durable media-write intents and a seventh maintenance kind, `generation_output_cleanup`. Deploy API, worker, migration and `metrics-alert-check.sh` together. Stop and drain older API/worker instances, including outstanding external writes, before applying it; the migration refuses running jobs and the database rejects generation writers and job claimers without `app.generation_output_protocol=journal-v1`. `database.Open` declares this automatically. Do not enable the declaration on an old binary to bypass the guard. Downgrade is refused once the journal has any rows; deleting rows to force downgrade would lose cleanup evidence.

The worker scans at startup and every minute, in an independent ten-second pass, selecting up to 100 due records. A new write intent is eligible after fifteen minutes; result writes and cleanup share the account lifecycle and generation locks. Successful attachments never enter this sweeper. Failed checks retry after one minute; active legal holds and conflicting content retry after one hour. Interrupted checks use a separate two-second context to persist backoff when possible. Due ordering prevents a recorded failure from continually monopolizing the first page. Persisted errors remain visible even if a subsequent pass has no due records.

| Metric | Meaning / alert |
| --- | --- |
| `hcai_generation_output_cleanup_failed` | Non-attached records with cleanup errors or unexpected references/bytes; any nonzero value alerts |
| `hcai_generation_output_cleanup_due` | Locations due for cleanup or another absence check |
| `hcai_generation_output_cleanup_oldest_due_age_seconds` | Longest overdue check; alerts after `ALERT_MAX_GENERATION_OUTPUT_CLEANUP_AGE_SECONDS` (default 300) |
| `hcai_maintenance_*{kind="generation_output_cleanup"}` | Existing startup, failure, staleness and clock checks also apply to this scan |

Inspect unresolved records with a database operations account using IDs, state, `last_error_code`, `cleanup_checks` and `next_check_at`. Do not publish storage keys or raw adapter errors in alerts. Restore the configured storage service for transient failures; automatic retries preserve the same intent. A `generation_output_conflict` requires investigation of the recorded generation and any asset/contract reference or unexpected bytes. Do not delete a conflicting object, overwrite its checksum, revoke a legal hold, or edit journal state merely to clear an alert. Active holds use `generation_output_retained`, which is not counted as a storage failure.

Cleanup verifies exact recorded bytes when present, deletes only unreferenced/unattached locations, and then requires the primary store to report absence. Cleaned records are immutable tombstones and are rechecked hourly because a timed-out PUT may finish later. A deletion receipt proves observed primary-store absence at that time, not recall of in-flight remote writes, removal of remote versions/backups, or erasure from a generation provider. Account deletion also checks these locations before issuing its receipt; failed cleanup keeps deletion in processing for the existing retry/recovery path.

This journal covers new writes after cutover. It does not locate pre-migration orphan objects or recover an already lost provider response. A generation worker retry may invoke the provider again and produce different output; the final database charge/asset is committed once. Journal retention, tombstone scan capacity, large-file verification under the configured staging budget and real S3 behavior still need production acceptance. The current test suite uses isolated databases and local or simulated storage and does not deploy this migration.

### Generation execution recovery (0117)

The independent `generation_execution_recovery` maintenance pass runs at startup and once per minute (100 candidates, 10-second budget). `RecoverExpired` remains responsible for queue lease recovery; business reconciliation requires a bound failed job and matching finished attempt. A missing job, unrelated duplicate failure, successful job with unfinished business state, or malformed evidence does not authorize release.

- `hcai_generation_recovery_due`: recoverable unfinished generations due for a check.
- `hcai_generation_recovery_oldest_due_age_seconds`: age since the oldest due check; zero when there are none.
- `hcai_generation_recovery_failed`: unfinished generations whose last recovery failed; retries back off one minute and clear the error after success.
- `hcai_generation_recovery_unresolved`: unfinished generations missing a binding, or attached to a terminal job without sufficient matching failure evidence. Investigate original job/attempt, reservation and generation records; do not rewrite them to quiet the alert.

The alert script requires all four series, checks count/age consistency, and alerts on failures, unresolved evidence, and excessive due age (`ALERT_MAX_GENERATION_RECOVERY_AGE_SECONDS`, default 300). Common maintenance signals detect a never-successful, failed, future-dated or stale pass. Backoff and lifecycle-lock skips do not mean the obligation was completed.

Recovery preserves prior job/attempt evidence and commits generation failure, both reservation releases and the durable failure-evidence job together. Account/reservation disagreement rolls everything back. Notification delivery is separate; if its evidence/delivery job exhausts attempts, follow the existing failed-job alert and recovery process. Metrics do not expose account IDs, prompts, storage keys or Provider error payloads.

## Rejected product webhook evidence (0118)

- `hcai_product_webhook_quarantines{mode="live|test"}` counts pending signed product receipts rejected by transaction verification.
- `hcai_product_webhook_quarantine_oldest_age_seconds{mode="live|test"}` measures age from original receipt time. Rechecking must not reset the age. Empty queues report zero.

The alert checker requires both series for each selected payment mode. Any pending count raises `product_webhook_evidence_pending_<mode>`; a zero count with nonzero age is an invalid metrics response. Labels contain only the environment, never a payment/account ID, raw event or operator reason. These gauges count all pending quarantines, including unassociated claims; they do not count only payments currently held for review.

Investigate in the finance evidence section, verify the original provider transaction, and recheck the stored receipt with its current version and an explanation. A remaining conflict stays pending. Successful admission links the ordinary provider event; inspect that event and the associated order/refund to verify final processing. Do not delete evidence, change signed fields, clear holds or resend refunds to silence an alert. Invalid-signature or pre-normalization rejections are not measured by this queue; zero pending receipts does not prove complete reconciliation or complete webhook security coverage.


### Upload scan execution recovery (0121)

The independent `asset_scan_execution_recovery` pass runs at startup and every minute, with 100 candidates and a ten-second execution budget. Shared `hcai_maintenance_*` metrics and `metrics-alert-check.sh` report missing success, last failure and stale success for this kind. Recovery errors are stored as `scan_recovery_failed` with a one-minute backoff; audit `asset.scan_recovered` records the original job ID. No scanner secrets, file contents or raw errors enter these metrics.

Only a bound failed original job with a matching finished attempt can turn pending into review. A successful maintenance heartbeat does not prove that historical missing/duplicate scan jobs have been resolved. Investigate pending uploads without `asset_scan_executions`, or with inconsistent job/attempt evidence, through the permission-scoped media queue. Do not reset job history or invent a clean verdict. Recovery preserves queued retries and manual review and performs no external scanner call. See [the execution contract](../docs/resource-marketplace-flows.md#627-扫描执行绑定租约复核与中断恢复0121).


#### Scan business backlog and evidence gaps

Deploy the API and alert checker together. In addition to the maintenance heartbeat, `/metrics` requires all six series below, backed by one query snapshot over pending uploads, immutable bindings and original jobs/attempts:

| Metric | Meaning / alert |
| --- | --- |
| `hcai_asset_scan_pending` | All pending uploads, including historical unbound uploads |
| `hcai_asset_scan_oldest_pending_age_seconds` | Age since oldest pending upload creation; `ALERT_MAX_ASSET_SCAN_PENDING_AGE_SECONDS`, default 900 |
| `hcai_asset_scan_recovery_failed` | Pending uploads with a failed recovery check; any nonzero value alerts |
| `hcai_asset_scan_recovery_due` | Evidenced failed original jobs whose recovery check is due |
| `hcai_asset_scan_recovery_oldest_due_age_seconds` | Age since the oldest due `recovery_after`; `ALERT_MAX_ASSET_SCAN_RECOVERY_AGE_SECONDS`, default 300 |
| `hcai_asset_scan_recovery_unresolved` | Missing/inconsistent execution evidence or invalid/future creation times; any nonzero value alerts |

A failed recovery in backoff still counts as failed, and its asset's original pending age is preserved. Queued retries and running attempts with matching evidence are not unresolved; expired matched leases remain covered by the shared lease-recovery signals. Reviewed/completed assets leave the pending aggregates. Investigate original evidence in the media queue instead of fabricating a binding or clean verdict.

Threshold equality is accepted. Missing/duplicate/non-numeric/negative series, fractional counts, count/age inconsistencies or subcounts exceeding pending fail the checker. SQL failures produce HTTP 503, not a healthy partial scrape. These gauges expose no account/asset identifiers, filenames, locations or upstream payloads. They introduce no migration beyond the required 0121 baseline and have not been deployed to the running environment. Isolated verification is recorded in [6.28 and 11.90](../docs/resource-marketplace-flows.md#628-扫描业务积压与证据缺口告警); production scrape latency and capacity remain to be validated.


#### Lock contention during execution recovery

Scan and generation recovery now skip locked binding candidates and busy lifecycle/business/job rows. A still-due, evidenced failed execution skipped during recovery receives a one-minute deferral when its binding can be locked without waiting. Deferral preserves `recovery_checks`, `last_error_code`, the original job and its attempts; it is not a success receipt. Failed-pass backoff recording also skips busy bindings rather than extending the lock wait.

This allows later candidates to progress across bounded passes. Original upload pending age and prior recovery failure remain visible; a healthy maintenance pass is not proof that every locked obligation has completed. Investigate persistently locked transactions rather than resetting evidence, granting clean status or resending external requests. No new migration is introduced; deploy the updated worker against the 0117/0121 baseline. See [rules and regression](../docs/resource-marketplace-flows.md#629-扫描与生成恢复的锁冲突和队列推进).

## Upload write journal and cleanup (0122)

The worker runs `upload_write_cleanup` at startup and every minute, at most 100 due locations under a ten-second pass budget. Unattached intents start with a fifteen-minute deadline. Storage failures and interrupted checks back off one minute; holds, conflicting references/bytes and verified-absent tombstones are checked again after one hour. Attached locations remain governed by ordinary asset and transaction retention. Checks against busy owners defer under the intent lock; competing cleaners skip locked rows rather than changing an in-flight cleaner's deadline.

| Series | Meaning / alert |
| --- | --- |
| `hcai_upload_write_cleanup_failed` | Non-attached writes with cleanup failure or conflicting references/bytes; any nonzero value alerts |
| `hcai_upload_write_cleanup_due` | Unbound locations or tombstones currently due for a check |
| `hcai_upload_write_cleanup_oldest_due_age_seconds` | Oldest overdue check; alerts above `ALERT_MAX_UPLOAD_WRITE_CLEANUP_AGE_SECONDS` (default 300 seconds) |
| `hcai_maintenance_*{kind="upload_write_cleanup"}` | No successful pass, last-pass failure, stale success and invalid timestamps use the shared checks |

Equality at the age threshold is allowed. Missing, repeated, nonnumeric or fractional-count series and inconsistent zero-count/positive-age combinations fail the check. A failed SQL query returns HTTP 503; a healthy heartbeat does not suppress business backlog alerts. Series contain no user/asset identifiers, filenames, hashes or private locations.

Deploy API, worker, 0122 and the checker together; older scrapes lack required series and should not be reported as healthy. Do not clear errors, delete journal rows or infer successful deletion from Delete acknowledgements. Inspect references/holds and the original object's identity before restoring storage access. Retained data is not a storage failure; conflict requires investigation, not forced deletion. Tombstones intentionally remain after a verified absence to detect late remote writes. Provider versions/backups and out-of-band restores still require separate inventory and acceptance. Production scrape latency, resource limits and real-storage acceptance remain unverified; see [scope and tests](../docs/resource-marketplace-flows.md#631-上传写入日志与未绑定原件清理0122).

## Media write age and cleanup contention

Both write journals expose the following required triples, with prefixes `hcai_generation_output` and `hcai_upload_write`:

| Suffix | Meaning / alert |
| --- | --- |
| `_pending` | Pending unattached writes without a currently active, unexpired legal hold |
| `_oldest_pending_age_seconds` | Age from immutable original `created_at`, independent of lock deferral or retry deadlines |
| `_pending_invalid_timestamps` | Pending writes with future or non-finite registration times; any nonzero value alerts |

`ALERT_MAX_MEDIA_WRITE_PENDING_AGE_SECONDS` defaults to 1800 seconds; equality is allowed. The checker emits `generation_output_pending_overdue` / `upload_write_pending_overdue` or the corresponding `_pending_clock_invalid` alerts. Missing, repeated, invalid or inconsistent series fail the check. The two triples come from one database statement and contain no owner IDs, paths, hashes or content. Query failure must not produce a healthy partial scrape.

An active legal hold excludes intentionally retained pending writes from this age alarm. Release or wall-clock expiry restores the original age immediately, even before maintenance updates an old retained error code. This is age since registration, not time spent without a hold. Cleaned tombstones are excluded from pending age but remain in the existing due-check metrics to detect late writes; attached files remain under asset/contract retention. Existing cleanup failure signals remain independent.

Generation cleanup now skips locked intent candidates and tries lifecycle/generation locks without waiting. Busy owners or generations receive a one-minute deferral under the intent lock, preserving check counts and errors. Concurrent cleaners cannot change an in-flight cleaner's schedule. Interrupted bookkeeping also skips locked intents. This prevents a locked first candidate from monopolizing small batches, while original pending age exposes persistent contention even when due count is zero and the maintenance heartbeat is healthy.

No new migration is required beyond the existing generation/upload journal baseline. Deploy the updated worker, API and checker together; an older API lacks the required triples. Investigate persistent locks and original write evidence rather than deleting journals or overriding retention. Production scrape cost, real storage, unknown objects and backup cleanup remain unverified. See [rules and validation](../docs/resource-marketplace-flows.md#633-生成文件清理锁冲突与写入原始年龄告警).

## Missing checkout checks (0124)

`product_checkout_reconciliation` runs immediately on worker startup and every minute, independently bounded to 100 candidates / 10 seconds per pass. Shared maintenance counters detect missing/failed/stale passes. Pair the heartbeat with `checkout_check_missing`, `checkout_check_stopped`, checkout age and event processing metrics; a pass can safely skip locked rows or unavailable/disabled readers without resolving any payment.

The scan only creates missing authenticated Stripe reads using immutable original merchant/session evidence. It never recreates a checkout, resets historical failed/cancelled jobs or declares financial success. Pending/failed provider evidence is handled by the existing replay path. Dispatch, version, event and audit commit together, and owner exports include the minimal dispatch link. Ship migration 0124, API, worker and the updated checker together; the checker deliberately rejects missing series from an older API. No running database has been upgraded. Real-provider, large-history query cost and paging acceptance remain outstanding.


Source reversal monitoring (0171) adds `seller_reversal_reconciliation` to the required maintenance-health series. It runs at startup and each minute with an independent ten-second budget. Coordinate migration, API, worker and checker releases; `database.Open` declares `app.seller_reversal_execution_protocol=journal-v1`. New-code maintenance against an old schema is not a valid deployment. Never-succeeded, failed, stale and missing series are covered by the alert-script regression. The financial reversal backlog, closure-due backlog and review/read-gap metrics are emitted separately and must not be inferred from maintenance health alone. Any first-send evidence blocks 0171 downgrade; preserve it. Isolated execution, recovery and migration tests pass, but full-current-source and real-provider acceptance remain incomplete. Internal ledger closing and the seller-facing status projection are added in 0172 below. See the source reversal section of `docs/resource-marketplace-complete-flow.md` and `docs/resource-marketplace-source-reversal.md`.

Source return closing (0172) requires `app.seller_reversal_closure_protocol=closure-v1` and coordinated financial writers. The internal explicit confirmation service consumes a full return into reservation release or exact refund-debt recovery; it does not call the provider. Subsequent manual or automatic transfers use persisted request/batch keys, while original financial history remains retained. Seller payout responses expose only `pending`, `observed`, `requires_review` or `closed` plus safe timestamps and resolution; provider evidence, operator identity, reasons and command keys remain private. Accepted observations and closure outcomes create idempotent seller notifications, and the owner data export includes allowlisted command/read/result/closure metadata without provider payloads. Isolated staged tests passed, and the fixed-current-source full payments run is still pending. Finance HTTP/UI closure action, real-provider acceptance, partial returns, contradictory-evidence adjudication and external bank reconciliation remain open. Do not enable this as a completed production reversal feature or bypass downgrade evidence retention.
