# ADR 0004: Identity sessions and transactional in-app notification evidence

## Status

Accepted for CP-05.

## Context

Identity and notifications cross every user workflow. A convenient browser-only login fallback would hide authentication failures, and an in-memory notification event could be lost after the business transaction commits. External OAuth and email delivery also require credentials and operational review that are not available in the local environment.

## Decision

- Email passwords use bcrypt. Session tokens are random values returned only to the browser and stored in PostgreSQL as SHA-256 hashes.
- Browser sessions use HttpOnly, SameSite cookies. Session evidence exposes a client label, recent activity, and a shortened hash-derived network hint, never a raw token or IP address.
- Roles and permissions are persisted. Resource ownership checks remain mandatory even when a role contains a broad permission.
- Registration, login, profile changes, session revocation, and security-relevant actions write audit evidence.
- Google and GitHub are explicit provider boundaries. They remain unavailable and fail closed until external credentials and staging verification are complete.
- Authentication requires a personal account. Shared login and actor switching have been removed; a `401` never creates an identity. Automated tests use isolated fixtures and ordinary password login.
- In-app notifications are user-scoped PostgreSQL records. Deep links accept only allowlisted internal routes and never accept external origins, query redirects, or fragments.
- Opening a notification and marking it read are separate user actions.
- Task, marketplace, and generation producers write notifications inside the same transaction as the state change. A unique user/source key makes producer replay idempotent.
- Notification preferences use optimistic versions. Only version 1 may create an absent preference; later writes must match the stored version.

## Consequences

- Identity and inbox evidence survives process restarts and cannot be inferred from another account.
- A committed purchase, refund, generation result, or task transition cannot lose its corresponding enabled in-app notification.
- Disabled notification categories suppress future inbox rows without deleting existing evidence.
- Production OAuth, verification email, password recovery, outbound delivery attempts, and bounce handling remain external or later checkpoints.
