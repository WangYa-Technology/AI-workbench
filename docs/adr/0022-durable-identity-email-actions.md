# ADR 0022: Durable identity email actions

## Status

Accepted on August 11, 2026.

## Context

Email verification and password recovery are security-sensitive workflows that must survive process restarts without exposing whether an account exists. The local product needs a reproducible delivery path, while production delivery cannot be claimed without an approved Provider, bounce/complaint processing, suppression policy, and operational acceptance.

## Decision

- PostgreSQL is the source of truth for verification and password-reset actions, delivery attempts, expiry, dead-letter state, optimistic versions, and durable jobs.
- Each action receives a 32-byte random token. PostgreSQL stores a SHA-256 lookup hash plus AES-256-GCM ciphertext bound to the action ID and kind. The configured encryption key must decode to exactly 32 bytes.
- Only one queued or delivered action of a kind may exist for a user. Reissue atomically cancels prior actions and erases their token hash, nonce, and ciphertext.
- Verification expires after 24 hours and password reset after one hour. Confirmation is one-time. Successful password reset updates the bcrypt credential and revokes all sessions.
- Password-reset request responses never reveal whether the submitted address is registered or active.
- Development delivery uses owner-only `.eml` files below `MEDIA_ROOT/mailbox/<user-id>`. Production requires `EMAIL_DELIVERY_MODE=disabled`; `local_file` is rejected in production until a separate approved adapter exists.
- Delivery attempts are append-only and retain only bounded adapter status, safe error code, receipt hash, and timestamp. Message bodies and Provider response bodies are not persisted as operational evidence.
- Admin dead-letter retry and cancellation use a separate high-risk permission and require a reason, explicit confirmation, exact expected version, owner notification where applicable, and immutable audit evidence.
- APIs, logs, audit metadata, notifications, Admin projections, and data exports exclude raw tokens, ciphertext, message bodies, confirmation URLs, and complete addresses. Data export includes masked metadata and receipt hashes only.
- Account deletion erases token material, minimizes recipient snapshots, cancels pending jobs, and removes the user's local mailbox directory while retaining bounded structural delivery evidence.

## Consequences

- Local verification and recovery can be tested end to end without an external service or an in-memory queue.
- A compromised database is not sufficient to read an unexpired action token without the separately configured AES key, while the token hash still supports constant-shape lookup.
- Restart recovery, expiration, retries, and administrator intervention are observable and auditable.
- Production email is intentionally unavailable until a Provider adapter and bounce/complaint lifecycle are reviewed and implemented. This is an external acceptance boundary, not a simulated success path.
- The migration down path is structurally reversible before identity email evidence is required for production retention. Data already removed by token erasure or account deletion cannot be reconstructed by rollback.
