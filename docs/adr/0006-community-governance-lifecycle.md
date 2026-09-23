# ADR 0006: Community interaction and reversible governance lifecycle

> 2026-09-18 implementation update: migration 0073 enforces reason, confirmation, expected version, transaction-local Admin audit, immutable governance events, viewer-scoped appeals, and per-report resource holds. Upheld no-action appeals reopen review; unsafe restoration returns a conflict. See [社区全链路说明与验收记录](../community-flows.md) for the implemented contract, C01–C13 acceptance evidence, and legacy-case boundaries.

## Status

Accepted on August 11, 2026.

## Decision

- Comments, likes, bookmarks, and follows are persisted relational records. Reaction and follow commands are idempotent, and Community post counts plus viewer state are derived from those records.
- Reports are user-scoped governance cases with one open case per reporter and resource. Reporters and subject authors can inspect relevant cases without receiving access to the Admin queue.
- A report decision requires the `admin:governance` permission, a specific reason, explicit confirmation, and an expected resource version. The same transaction updates the report, applies any content visibility change, writes a governance event and Admin audit event, and creates user notifications.
- Hide and remove outcomes create independent per-report holds with versioned resource baselines. An upheld appeal releases only that report’s holds; restoration requires no competing hold, no newer content mutation, an active author, safe media, and no owner withdrawal. An upheld no-action appeal reopens the report for a new decision round. A denied appeal leaves content unchanged.
- Notifications deep-link only to allowlisted internal Community routes. Localized labels explain notification categories, while stored notification bodies remain durable decision evidence.

## Consequences

- Community controls have real persisted state and remain consistent after reloads or across sessions.
- Moderators cannot silently mutate content through the governance queue: every decision has actor, reason, request, resource, status transition, and user-visible evidence.
- Appeals are operational rather than cosmetic because a successful decision safely releases the corresponding restriction or reopens a no-action review in the same transaction; stale or unsafe restoration is rejected.
- The current lifecycle governs Community posts and supports work/comment resource types at the service boundary. Dedicated media scan cases, automated risk signals, and copyright/support intake remain separate checkpoints.
