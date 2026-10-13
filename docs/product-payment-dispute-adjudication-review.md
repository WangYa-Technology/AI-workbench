# Product Payment Dispute Adjudication Review

Date: 2026-09-23

## Ownership and authority

- Stripe is the external authority for dispute lifecycle facts. Only signature-verified Stripe webhook evidence or a complete, immutable Stripe read may establish `won`, `lost`, or `withdrawn`.
- The payment event worker owns projection and financial side effects inside one PostgreSQL transaction. Webhook ingress only verifies, minimizes, and queues evidence.
- Finance operators with active `admin:finance` may route a case, request evidence, record an opaque provider receipt reference, or escalate a recovery review. They cannot set provider status, adjudicate the dispute, create a refund, change entitlements, or edit balances.
- Buyer access is changed only when complete terminal Provider evidence is bound to the exact product payment. Seller recovery is represented by `product_settlements` and `seller_recovery_obligations`, never by a direct wallet adjustment.
- Real Stripe acceptance and deployment are separate release gates; isolated database evidence is not production authorization.

## Current source review

`internal/transport/httpapi/stripe_product_webhook.go` verifies the Stripe signature and stores minimized evidence. `internal/payments/workflow.go` claims and processes the event transactionally. `internal/payments/product_dispute_binding.go` requires an exact provider/mode/PaymentIntent/Charge/amount/currency tuple; zero or multiple matches remain review-only. `product_dispute.go` maintains immutable event history and stable `(occurred_at, provider event UUID)` ordering.

The target 2 finance command in `internal/admin/product_disputes.go` is limited to internal routing/evidence bookkeeping. It is not an adjudication command. Existing refund success handling in `refundProductPaymentTx` is the canonical buyer-rights revocation path; `markProductSettlementRefundTx` (now sharing `markProductSettlementReversalTx` with dispute loss) serializes seller funds, holds unpaid amounts, and creates recovery obligations after transfer or payout-at-risk. `ensureSellerSettlementCreditTx` writes only to the seller ledger, and entries are immutable.

## Migration audit

| Migration | Tables/functions/triggers | Adjudication relevance |
| --- | --- | --- |
| 0143 product seller settlements | `product_settlements`, settlement dispatch/event evidence, immutable settlement-event trigger | Settlement snapshot is separate from wallet and records payout lifecycle. Later recovery must not rewrite its historical transfer evidence. |
| 0151 seller funds ledger | `seller_ledger_entries`, `seller_recovery_obligations`, immutable ledger guard, balance/recovery projections | Credits, reservations and recoveries have distinct identities. Dispute handling must not write a wallet balance or mutate ledger entries. |
| 0173 product payment disputes | minimized provider event columns; dispute root/event tables; immutable dispute-event guard; settlement hold function/trigger | Root rows are projections; event rows are evidence. Hold guard prevents unresolved disputes entering provider settlement dispatch. |
| 0174 stable ordering | `latest_event_id`, backfill and FK | Event time ties use provider event UUID, allowing deterministic ordering and late-event retention. |
| 0175 operations | internal operation and evidence-submission tables; immutable triggers | These are operator audit facts, not Provider facts and cannot be interpreted as a decision. |
| 0176 reason check | strengthens immutable operation reason constraints | Retains explainable operations; no money effect. |
| 0177 reference checks | opaque receipt reference checks | A receipt reference proves only a recorded reference, not external submission or outcome. |

Triggers are transaction guards, not authorization boundaries by themselves. The new adjudication path must lock the bound payment/order/settlement in a documented global order, use compare-and-set/idempotency, and commit operation evidence, financial projections, entitlement changes, and audit together. Evidence-preserving down migrations must refuse rollback after adjudication effects exist.

## STRIDE threat model

| Threat | Example | Required control / test |
| --- | --- | --- |
| Spoofing | Forged `charge.dispute.closed`, wrong Stripe mode or another seller's payment tuple | Signature verification, webhook identity, exact immutable tuple binding; wrong-signature/mode/charge/amount tests leave all financial state unchanged. |
| Tampering | Reuse event ID with altered body; mutate terminal status with an older event | Stored payload digest equality, immutable provider evidence, stable ordering; duplicate and late-event tests prove no duplicate side effect or regression. |
| Repudiation | Operator claims evidence was submitted or a decision was made | Immutable operation/audit record distinct from Provider evidence; operator API cannot set terminal status. |
| Information disclosure | Return buyer payment details, raw Stripe payload, evidence files, or credentials in finance directory | Minimized DTO, opaque references, no raw payload; HTTP projection tests assert sensitive fields absent. |
| Denial of service | Flood duplicate disputes or lock settlement/refund indefinitely | Unique provider event/root keys, bounded transactions, row/advisory locks and retryable conflicts; duplicate/concurrency tests. |
| Elevation of privilege | Member or revoked finance actor adjudicates through replay or direct route | Active `admin:finance` checked and pinned through commit; unauthorized/revoked/replay tests. No human adjudication endpoint exists. |

## Financial decision rules

1. Nonterminal statuses (`needs_response`, `under_review`, warning variants), `requires_review`, incomplete/ambiguous binding, and unknown terminal-like values preserve the hold and create no buyer/seller money effect.
2. `won` is terminal Provider evidence. It never undoes a refund, entitlement revocation, completed payout, or recovery obligation. It may release only a `dispute_hold` when no other unresolved dispute and no refund attempt in `requested`/`pending` or reconciliation-required state exists. The normal availability window still applies.
3. `lost` and `charge_refunded` are Provider-confirmed dispute loss. Revoke the bound order's active entitlement exactly once and mark order/payment as disputed/refunded only if no refund is already pending or conflicting. Do not issue a second Provider refund. Before irreversible seller payout, cancel the payable. After settlement transfer, independent payout, or uncertain payout dispatch, create or retain a recovery obligation for the net amount under the existing seller-funds lock and recovery model.
4. `withdrawn` or another explicit nonterminal/withdrawn event remains frozen until Stripe supplies a definitive outcome. It must not be mapped to `won` or `lost` by operator input.
5. Any full refund already underway, multiple successful refunds, contradictory refund/dispute evidence, or competing terminal observations causes `requires_review`/reconciliation and retains both buyer/seller protections. The canonical refund pipeline remains the only path for Provider refund results.
6. Several disputes may bind one settlement/payment. One `won` cannot release a hold while another dispute is unresolved. A lost case is idempotent by immutable dispute decision identity and cannot duplicate entitlement, settlement, recovery, notification, or audit effects.

## Test and release plan

- Empty-schema migration and migration round-trip/evidence-preserving down guard.
- Isolated PostgreSQL end-to-end fixtures for unique/zero/ambiguous binding; won/lost; open sibling dispute; refund requested/pending/succeeded; payout queued/unknown/transferred; immutable evidence and rollback.
- `go test -race` for duplicate, equal-time/late provider events, two competing terminal outcomes, dispute-vs-refund, dispute-vs-settlement dispatch, and competing disputes on one settlement.
- HTTP tests prove operator commands cannot manufacture Provider terminal outcomes or trigger refunds/ledger writes; role revocation and audit rollback remain covered.
- Verify buyer order/entitlement, seller settlement/recovery, immutable histories, audit, notifications and cleanup queue as one transaction. Inject failure at each write boundary and prove total rollback.
- Document operational reconciliation for contradictory evidence and recovery debt. No live Stripe, bank, or production database is used by these tests.

## Implementation and audit — 2026-09-23, target 3

`0178_product_dispute_adjudication` adds one immutable adjudication per dispute, an immutable Provider-event reference, outcome and financial-effect classification. Its composite FK and insert trigger require the same dispute, status, provider, mode, PaymentIntent, Charge, amount, currency and product order. The down migration refuses to discard adjudications or upgrade replay jobs. Existing terminal roots are queued using their original latest event; `HandlePaymentEventJob` can process a previously consumed event only when its bound terminal root has no adjudication. Normal completed-event replays remain no-ops.

`handleProductDisputeEventTx` keeps event history, rejects changed binding tuples, locks payment then order, takes the seller funds lock before settlement, and applies decisions through `applyProductDisputeAdjudicationTx`. The worker's serializable transaction may return `40001`; the durable job must retry the whole transaction, not any individual write. Competing terminal won/loss evidence always changes the action projection to `requires_review`, including late evidence. A prior irreversible effect is never undone. Equal-time ordering remains deterministic for the status projection, while the contradiction rule is independent of ordering.

| Provider fact and local context | Atomic effect |
| --- | --- |
| Latest bound `won`; paid/fulfilled; no pending or unresolved refund; no other open dispute; settlement in dispute hold | Restore the normal pending/available hold window. No ledger credit, refund or entitlement grant. |
| Latest bound `won` with a refund/reconciliation conflict | Keep financial protections and mark `requires_review`; never undo a confirmed refund or recovery debt. |
| Latest bound `lost` / `charge_refunded`; paid/fulfilled; no pending/refund-check conflict | One `dispute.loss.applied` payment event, order event, entitlement revocation, cleanup job, buyer notification and audit; mark order/payment `refunded` as the existing closed-access projection. No Stripe refund ID is fabricated and no refund request is sent. |
| Loss before seller transfer | Existing seller credit history stays immutable; `refund_hold` excludes the payable from withdrawal. |
| Loss after automatic transfer, independent source funding, completed bank payout or unknown bank response | The existing seller-funds transaction creates one settlement-scoped net recovery obligation. No wallet balance edit or duplicate debt. |
| Loss while refund is requested/pending, reconciliation is unresolved, or a separate full refund already succeeded | Record review-only decision and keep the hold. Existing authenticated refund confirmation may still revoke rights; it is not replaced by a synthetic refund. |
| `withdrawn`, unknown or unsupported lifecycle value | Preserve Provider status, use action `requires_review`, no terminal decision or funds effect. A later explicit won/lost may be processed. Withdrawal is not proof of a win. |
| Older event | Retain immutable evidence; do not apply a stale financial decision. An older contradictory won/loss still forces review. |
| Another dispute already applied a loss to this order | No second revocation or recovery. |

`BeginProductRefund` and `createProviderRefund` also check unresolved disputes under the payment/order locks. A refund already requested before dispute processing can remain pending, but a new outbound dispatch is blocked. A refund already sent is reconciled through the existing Provider refund pipeline. Finance operations remain routing, opaque evidence references and recovery escalation only; there is no local adjudicate or balance-edit endpoint.

Review transitions additionally record `marketplace.provider_dispute_review`. Terminal audit actions are `marketplace.provider_dispute_adjudicated` (Provider outcome and observed effect classification) and `marketplace.provider_dispute_lost` (applied buyer loss). Original immutable payment/dispute and seller settlement/ledger histories remain the financial evidence. `release_hold` describes eligibility at that observation; later events may retain/reapply protection. Review decisions are sticky and are not silently upgraded by retries.

The pre-implementation test plan above is a coverage plan, not a claim that every write boundary or every production state has been exercised. The implemented isolated tests inject an audit failure and prove transaction rollback, and exercise actual shared recovery behavior after successful and unknown bank payout. Real Stripe lifecycle payloads, channel fees and production acceptance remain target 8.

## Executed validation

All database checks below used isolated test schemas, not production. No real Stripe or bank operation was invoked.

| Command / scope | Actual result |
| --- | --- |
| `go test -race ./internal/payments -run 'TestProductDispute\|TestIsNewerDispute\|TestProductRefundDurableDispatch' -count=1 -timeout=4m` | PASS, 87.619s on the final business-code revision. Includes adjudication, contradictory concurrent results, refund in flight/already succeeded, lost-response bank payout, audit rollback, migration round-trip/down guard, legacy replay and refund/settlement races. |
| `go test -race ./internal/payments -run 'Test(ProductDispute\|IsNewerDispute\|ProductRefundDurableDispatch\|SellerBankExecution.*\|SellerFunding.*)' -count=1 -timeout=12m` | PASS, 581.798s on the earlier revision before the final distinct settlement-event naming/legacy replay additions; final focused run above covers the completed adjudication code and shared refund wrapper. This is a selected regression run, not the full payments package. |
| `go test ./internal/admin ./internal/transport/httpapi ./internal/platform/database -run 'Test(ProductPaymentDispute\|MigrateEmptySchema)' -count=1 -timeout=4m` | PASS; includes finance authorization/operation audit and HTTP contract fixture. |
| `go test ./internal/platform/database -run '^TestMigrateEmptySchema$' -count=1` | PASS again after the upgrade replay migration change, 7.039s. |
| `go vet ./internal/payments` | PASS on final business code. |
| `npm --prefix web run test` | PASS, 33 files / 256 tests. |
| `npm --prefix web run typecheck` | PASS. |
| `npm --prefix web exec -- openapi-typescript internal/transport/httpapi/openapi.yaml -o web/src/api/schema.d.ts --check` | PASS; target 3 adds no operator result-setting endpoint or new HTTP status enum. |
| `git diff --check` | PASS. |

Initial fixture failures were corrected: Stripe IDs now satisfy the minimum length; transferred settlement fixtures include both transfer ID and timestamp; the refund race uses the normal refund-window purchase fixture. Concurrency tests retry only serialization/deadlock transaction failures with an explicit bound. No skipped assertion, disabled Race check or increased unbounded timeout was used. Full payments Race/full-repository verification remains target 5; deployment and real Stripe acceptance remain target 8.
