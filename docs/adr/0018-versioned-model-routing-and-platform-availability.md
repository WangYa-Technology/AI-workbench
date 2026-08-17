# ADR 0018: Versioned model routing and platform availability

## Status

Accepted on August 11, 2026.

## Decision

- Migration `0023_model_route_revisions` stores one immutable, independently versioned route history for each `chat`, `image`, `video`, and `music` mode. Administrators require `admin:models` to inspect or activate a route.
- Route activation requires the exact active version, a bounded reason, explicit confirmation, timeout and retry bounds, and a Provider profile for the same mode. External profiles fail closed until runtime verification exists; the current Local Test profiles remain the only activatable routes.
- Generation submit and retry resolve the active route inside their business transaction. Each new generation stores the exact route revision ID/version together with the Provider and model snapshot; historical generations are never reinterpreted after a route change.
- Migration `0024_system_setting_revisions` stores immutable atomic platform-availability revisions and an optimistic singleton pointer. Administrators require `admin:settings`, a bounded reason, explicit confirmation, and the exact active version.
- One revision controls registrations, generation submissions, Community publishing, Marketplace checkout, task publishing, and a bounded public notice. All five write gates default to enabled so migration preserves verified behavior.
- Each gated command checks the active revision inside its existing transaction after resolving a valid idempotent replay or previously owned result. A disabled capability rejects only a new write with stable `503 feature_disabled`; it does not invalidate previously committed evidence.
- Both control planes append the new revision, advance their active pointer, and write request-scoped Admin audit evidence in one transaction. Database triggers reject revision update and deletion. Both migrations provide complete structural rollback.

## Consequences

- Operators can change model selection and temporarily pause high-impact writes without a deployment while retaining exact actor, reason, version, and business-result evidence.
- Stale Admin clients, unverified external Providers, and disabled writes fail closed. Restoring availability creates another attributable revision instead of mutating or deleting the incident record.
- Route timeout and retry values are durable control-plane evidence. Provider adapter enforcement remains bounded by the current deterministic implementation and must be revalidated when real external adapters are introduced.
- The platform availability policy is intentionally global. Per-region maintenance, scheduled activation, approval quorums, and public status-page distribution require separately governed additions.
