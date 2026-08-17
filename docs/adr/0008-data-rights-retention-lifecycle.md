# ADR 0008: Data rights, retention, and delayed deletion lifecycle

## Status

Accepted on August 11, 2026.

## Decision

- A personal data-rights request requires an active session created within 15 minutes, exact confirmation of the account handle, and a rolling request limit. Shared demo identities cannot be exported or deleted.
- Data export is a durable worker job. It builds an owner-only JSON package, excludes credential/session/network secrets, enforces a 5 MiB bound, stores byte-exact SHA-256 evidence, and expires after seven days.
- Export expiry is executable retention rather than a display-only timestamp: a delayed job purges the artifact body under an explicit database maintenance setting, marks the request complete, and retains checksum/size/time evidence.
- Account deletion is delayed for 30 days and remains owner-cancellable before the deadline. Primary processing revokes sessions/OAuth, removes notifications and owned local media, denies Asset/content access, anonymizes identity and authored content, and preserves minimum transaction, audit, and safety facts.
- Owner exports include requester-visible Support cases, messages, and ordered state evidence while excluding operator identity and assignment metadata. On deletion, Support subject/details, rights statements, message bodies, resolution prose, and event free text are replaced with fixed redaction markers; category, state, timestamps, resource reference, resolution code, and pseudonymous relational integrity remain as minimum operational evidence.
- Support messages and events remain append-only during normal operation. A transaction-local data-rights maintenance setting permits only fixed redaction values while requiring every other row field to remain byte-equivalent, so it cannot be used as a general evidence-editing bypass.
- Completion creates an append-only per-domain receipt. The receipt explicitly reports production backup expiry and external Provider deletion as external boundaries; local execution never claims those systems were erased.
- `admin:data-rights` controls account-wide legal holds. Authority references are hashed before persistence; creation and release require a reason and explicit confirmation, notify the owner, write Admin audit evidence, and block or requeue deletion. Holds require review within 90 days and expire within 365 days.

## Consequences

- Export and deletion survive API/worker restarts and expose stable evidence instead of relying on synchronous UI actions.
- A data export cannot leak stored password hashes, session tokens, network hashes, or Admin-only package contents through its API contract.
- Support evidence can satisfy both access and deletion requests without silently retaining the requester's free-form case narrative. A legal hold continues to block the entire deletion transaction before redaction begins.
- Legal preservation is conservative and account-wide in this checkpoint. Production domain-scoped holds require an approved retention inventory and legal operating procedure before replacing this boundary.
- Production release still requires backup inventory/expiry verification, external Provider deletion receipts, outbound communications, and legal review; these remain external acceptance conditions rather than blockers to local lifecycle testing.
