# ADR 0025: Durable in-app notification delivery

## Status

Accepted on August 11, 2026.

## Context

Business transactions previously inserted inbox rows directly. That preserved producer idempotency, but it did not expose queued work, delivery attempts, preference-time suppression, or worker recovery. The platform objective requires notifications to use the same durable PostgreSQL task state machine as generation, scanning, email actions, Webhooks, and data-rights work.

Notification preferences can change after a business transaction commits and before its delivery job runs. Evaluating the preference at producer time would make delayed or recovered work ignore the user's latest choice.

## Decision

- `notifications.CreateTx` inserts one `queued` notification and one `notification.deliver` job in the producer transaction.
- `(user_id, source_key)` remains the producer idempotency boundary. Replaying a producer creates neither a second notification nor a second job.
- The worker evaluates the latest in-app preference while holding the notification row lock.
- Enabled notifications become immutable `delivered` records and enter the inbox.
- Disabled notifications become immutable `suppressed` records with the bounded code `preference_disabled` and never enter unread/read queries.
- Job claim attempts, heartbeats, fencing, expiry recovery, cancellation, and safe failure codes use the shared durable job repository.
- Owners can list recent safe delivery evidence. The projection excludes job payloads, lease owners, lease tokens, worker identifiers, and raw errors.
- Account deletion cancels queued and running notification jobs before deleting notification content. The shared cancellation trigger closes any running attempt as `cancelled` with `job_cancelled`.
- Existing seed and pre-migration inbox rows are backfilled as delivered evidence.

## Consequences

The inbox is eventually consistent with the originating transaction, while the business transaction itself remains atomic and replay-safe. A disabled category retains bounded evidence that the platform honored the preference without retaining an inbox item. Terminal delivery evidence cannot be rewritten.

External email, Slack, or generic outbound notification fan-out is not enabled by this decision. Identity email and Developer Webhooks keep their separate approved boundaries; any future notification channel requires explicit credentials, consent, suppression/bounce operations, data-rights handling, and production acceptance.

## Verification

- Empty-schema migration and migration idempotency tests cover migration `0033`.
- Notification repository tests cover producer replay, delivery, latest-preference suppression, safe invalid-payload errors, owner isolation, and terminal immutability.
- HTTP tests cover authenticated owner evidence, anonymous denial, and cross-account isolation.
- Data-rights tests cover the safe export projection and cancellation of queued/running notification jobs before content deletion.
- The Identity/Notifications browser workflow covers delivered attempt evidence and preference-driven suppression without an inbox item.
