# ADR 0024: Durable job attempts and heartbeats

## Status

Accepted on August 11, 2026.

## Context

The PostgreSQL job queue persisted job state and short worker leases, but a claim had no unique fencing token, long handlers did not renew their lease, and retry history was overwritten on the job row. A restarted or delayed worker could therefore race a replacement worker, while operators had no durable evidence that distinguished handler failure from worker lease expiry. Persisting raw Provider errors would expose untrusted or sensitive response content.

## Decision

- Every claim creates a random lease token and one append-only `job_attempts` row in the same transaction that moves the job to `running`.
- Renewal, completion, and failure require the job ID, worker owner, and lease token. A stale worker receives `ErrLeaseLost` and cannot mutate a replacement attempt.
- Workers renew at one third of the configured lease. Heartbeat failure cancels the handler context; handler completion independently stops heartbeats so cancellation of an in-flight renewal is not treated as a handler failure. The final repository compare-and-set remains authoritative.
- Expiry recovery atomically closes the running attempt as `lease_expired` and either requeues or terminally fails the job according to its existing attempt limit.
- A database trigger closes a running attempt as `cancelled`, clears its lease, and stores `job_cancelled` in the same transaction whenever any domain cancels the parent job. This applies one invariant to generation, email, Webhook, and data-rights cancellation paths.
- Attempt evidence stores only job kind, attempt number, a SHA-256 worker reference, renewal count, timestamps, terminal state, and a closed safe error code. Payloads, lease owners, tokens, and raw handler or Provider errors are excluded from Admin projections and structured failure logs.
- Terminal attempts reject updates and deletion. Migration rollback is allowed only while no attempt evidence exists; otherwise it fails explicitly rather than discarding operational history.
- Admin diagnostics expose only aggregate queue and 24-hour attempt counts, renewal totals, expirations, and terminal failures under `admin:observability`.

## Consequences

- Long-running creation, scanning, notification, email, Webhook, and data-rights jobs remain owned while a healthy worker is executing them.
- Process crashes are recoverable after lease expiry, with an auditable distinction between worker loss and handler failure.
- Business cancellation cannot leave orphaned running-attempt evidence, and the cancelled worker is fenced from later completion.
- At-least-once delivery still requires domain handlers to keep their existing idempotency and transaction boundaries. Heartbeats prevent concurrent stale completion; they do not claim exactly-once external side effects.
- Worker names remain useful locally but are represented only by irreversible hashes in durable evidence. Production fleet identity and centralized alerting remain deployment concerns.
