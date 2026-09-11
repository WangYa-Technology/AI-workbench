# ADR 0018: Versioned model routing and platform availability

## Status

Accepted on August 11, 2026.

## Decision

- Migration `0023_model_route_revisions` stores one immutable, independently versioned route history for each `chat`, `image`, `video`, and `music` mode. Administrators require `admin:models` to inspect or activate a route.
- Route activation requires the exact active version, a bounded reason, explicit confirmation, timeout and retry bounds, and a Provider profile for the same mode. External profiles fail closed until runtime verification exists; the current Local Test profiles remain the only activatable routes.
- Generation submit and retry resolve the active route inside their business transaction. Each new generation stores the exact route revision ID/version together with the Provider and model snapshot; historical generations are never reinterpreted after a route change.
- Migration `0024_system_setting_revisions` introduced atomic platform-availability gates. Migration `0059_remove_system_setting_revisions` replaces the global revision chain with one mutable `system_settings` singleton. Administrators still require `admin:settings`; updates are transactional and request-scoped audit evidence is retained, but there is no global version, expected version, or history cursor.
- The singleton controls registrations, generation submissions, Community publishing, Marketplace checkout, task publishing, and a bounded public notice. All five write gates default to enabled so migration preserves verified behavior.
- Each gated command checks the current singleton inside its existing transaction after resolving a valid idempotent replay or previously owned result. A disabled capability rejects only a new write with stable `503 feature_disabled`; it does not invalidate previously committed evidence.
- Model routing, ranking policies, risk rules, and other resource-level controls remain independently versioned and optimistic. The system-settings migration provides complete structural rollback by reconstructing a single v1 row from the mutable singleton.

## Consequences

- Operators can change model selection and temporarily pause high-impact writes without a deployment while retaining exact actor and business-result evidence.
- Unverified external Providers and disabled writes fail closed. Concurrent system-settings saves use the singleton row's transactional update semantics; resource-level version conflicts remain enforced where they matter.
- Route timeout and retry values are durable control-plane evidence. Provider adapter enforcement remains bounded by the current deterministic implementation and must be revalidated when real external adapters are introduced.
- The platform availability policy is intentionally global. Per-region maintenance, scheduled activation, approval quorums, and public status-page distribution require separately governed additions.
