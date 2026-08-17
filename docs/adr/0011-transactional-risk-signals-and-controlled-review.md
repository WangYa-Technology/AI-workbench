# ADR 0011: Transactional risk signals and controlled review

## Status

Accepted on August 11, 2026.

## Decision

- Risk signals are durable PostgreSQL records, not derived Admin-page warnings. A task dispute and a Local Test order refund create one idempotent signal inside the same transaction as the originating business state change.
- Each producer owns a stable source key. Replaying a dispute or refund cannot create duplicate signals or duplicate detection events.
- The initial bounded rule catalog is explicit and explainable: task disputes produce a high-severity score of 85, and Local Test refunds produce a medium-severity score of 55. Evidence snapshots contain only operational state and amount/currency facts needed for review.
- Migration `0018_risk_review` backfills existing disputed tasks and refunded Local Test orders so deployment does not silently omit pre-existing review work.
- Operators require `admin:risk`. Queue items expose a stable task/order title, subject, signal type, severity, score, status, detection time, ordered events, and an internal resource deep link.
- Review supports `monitor`, `no_action`, and `escalated`. Every command requires a reason, explicit confirmation, and the expected optimistic version. `no_action` and `escalated` are terminal and cannot be overwritten; `monitor` remains reviewable.
- Review events are append-only and every accepted command writes `admin.risk_reviewed` audit evidence in the same transaction. The Admin overview includes risk counts by status.

## Consequences

- Operations receives review work even if the browser is closed when a dispute or refund occurs, and producer replay cannot inflate the queue.
- Review history is attributable and conflict-safe across concurrent operator sessions.
- The initial rules are deterministic local policy signals, not fraud verdicts or opaque model scores. New signals, configurable thresholds, and production case escalation require explicit schema and policy revisions.
- The queue is bounded to 200 ordered records in this checkpoint. Server pagination and rule-management controls remain required before production-scale operations.
