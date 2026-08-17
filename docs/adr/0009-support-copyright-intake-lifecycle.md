# ADR 0009: Support and copyright intake lifecycle

## Status

Accepted on August 11, 2026.

## Decision

- Support cases are private, requester-owned records with categories for general support, billing, account access, task/order help, and copyright intake. Requesters can list, inspect, and reply only to their own cases.
- Copyright intake requires a stable published Work, Product, or Community Post reference, a claimant relationship, and a bounded rights statement. The platform does not collect government identifiers, payment card data, signatures, passwords, or raw legal documents, and the workflow is explicitly platform intake rather than legal adjudication.
- Case status follows `open -> in_review/waiting_for_requester -> resolved/closed`, with an explicit controlled reopen from `resolved` to `in_review`. Every mutation increments an optimistic version so concurrent requester and operator actions fail with a conflict instead of overwriting evidence.
- Messages and events are append-only at the database level. User and Admin mutations write audit evidence in the same transaction. Admin replies and state decisions require `admin:support`, a specific reason, and explicit confirmation, and notify the requester through an allowlisted `/support/{id}` deep link.
- Data-rights export projects the requester's cases, messages, and ordered event history without exposing operator identity. Account deletion uses the constrained database maintenance path from ADR 0008 to redact free-form Support content while retaining minimum state, timing, resource, and resolution evidence.
- Community reports remain a separate content-governance lifecycle. Copyright intake can lead to a platform decision, but it does not silently mutate Community moderation records or claim a legal ownership determination.

## Consequences

- Users have one private thread and stable status evidence for support without exposing cases in Community or Search.
- Operators cannot silently edit or resolve a case: actor, reason, request ID, version, status transition, message, notification, and audit evidence remain attributable.
- Append-only is not used as a reason to retain requester prose indefinitely: the only permitted evidence update is a fixed account-deletion redaction that cannot alter status, timestamps, relationships, or event identity.
- Production legal review, jurisdiction-specific notices, statutory deadlines, outbound email, and external document exchange require separate legal acceptance and integrations; the local workflow does not pretend those steps occurred.
