# ADR 0006: Community interaction and reversible governance lifecycle

## Status

Accepted on August 11, 2026.

## Decision

- Comments, likes, bookmarks, and follows are persisted relational records. Reaction and follow commands are idempotent, and Community post counts plus viewer state are derived from those records.
- Reports are user-scoped governance cases with one open case per reporter and resource. Reporters and subject authors can inspect relevant cases without receiving access to the Admin queue.
- A report decision requires the `admin:governance` permission, a specific reason, and explicit confirmation. The same transaction updates the report, applies any content visibility change, writes a governance event and Admin audit event, and creates user notifications.
- Hide and remove outcomes preserve the prior content status. An upheld appeal restores that status atomically and records the reversal; a denied appeal leaves the existing moderation outcome intact.
- Notifications deep-link only to allowlisted internal Community routes. Localized labels explain notification categories, while stored notification bodies remain durable decision evidence.

## Consequences

- Community controls have real persisted state and remain consistent after reloads or across sessions.
- Moderators cannot silently mutate content through the governance queue: every decision has actor, reason, request, resource, status transition, and user-visible evidence.
- Appeals are operational rather than cosmetic because a successful decision reverses the corresponding content action in the same transaction.
- The current lifecycle governs Community posts and supports work/comment resource types at the service boundary. Dedicated media scan cases, automated risk signals, and copyright/support intake remain separate checkpoints.
