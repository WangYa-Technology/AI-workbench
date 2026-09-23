# ADR 0002: PostgreSQL demand task lifecycle and Local Test settlement

> 2026-09-18 implementation update: this ADR preserves the original local-ledger design. Current HTTP tasks require verified Provider funding. See [the current task lifecycle](../task-marketplace-flows.md) for participant privacy, delivery bundles and immutable grants, expiry/extension handling, asynchronous refunds/transfers, and the restored reason/confirmation/audit contract. The local settlement statements below are historical, not current payment behavior.

## Status

Accepted for CP-03.

## Context

The demand workflow crosses two users and several requests: a commissioner publishes a brief, a creator proposes or accepts directly, an owned asset is delivered, the commissioner reviews it, and the result is settled or disputed. Process memory and client-side status cannot preserve authorization, history, or financial invariants across retries and restarts.

## Decision

- PostgreSQL is the source of truth for task briefs, proposals, assignments, versioned deliveries, review notes, disputes, settlements, command idempotency, and events.
- Every transition is enforced by `internal/tasks.Service` using the authenticated actor, current task state, and a database transaction.
- Direct acceptance settles at the published reward. Proposal acceptance settles at the accepted proposal amount.
- Delivery references an Asset owned by the assignee with a clean scan state. Each delivery inserts a new version and preserves prior evidence.
- Acceptance writes one commissioner debit and one creator credit in the same transaction. A uniqueness constraint prevents duplicate ledger entries for the task.
- Dispute pauses settlement and records the actor, reason, and event. Automated dispute resolution and real payouts are outside this checkpoint.
- Commands accept an idempotency key so browser retries cannot duplicate proposals, assignment, delivery, review, or dispute transitions.
- The API keeps `local_test` as a stable machine value; the UI presents localized `Local Test USD` wording and states that no real funds move.

## Consequences

- Task history and settlement invariants survive API and worker restarts and can be audited from PostgreSQL.
- The frontend remains a role-aware projection rather than the authority for task transitions.
- Production payments require a separate provider adapter, compliance review, payout model, refund/dispute policy, and a new ADR. The Local Test ledger must never be reinterpreted as real money.
