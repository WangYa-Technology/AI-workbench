# ADR 0015: Evaluated Discovery candidates and staged rollout

## Status

Accepted on August 11, 2026.

## Decision

- Discovery continues to query live PostgreSQL business facts. The platform does not introduce a periodically refreshed document store that would delay newly published works, active products, creators, or open demands.
- Migration `0020_discovery_operations` adds partial `text_pattern_ops` expression indexes for the normalized fields used by the public query. It deliberately avoids a database-wide `pg_trgm` extension because isolated-schema integration tests may run concurrently against one PostgreSQL database.
- Ranking changes are created as immutable candidates beside the active baseline. Creating a candidate never changes public ranking by itself and is rejected while another rollout is active.
- A candidate must pass a deterministic offline relevance evaluation against the current baseline before receiving traffic. The gate requires candidate top-one hits and mean reciprocal rank to be no worse than baseline and records zero known safety violations.
- Public search assigns normalized queries to a deterministic cohort and reports `policyVariant`. Candidate exposure is limited to `0`, `5`, `10`, `25`, `50`, or `100` percent. Setting `0` stops exposure; setting `100` atomically promotes the candidate and clears rollout state.
- Index analysis runs and ranking evaluations are append-only operational evidence. Every Admin mutation requires `admin:ranking`, a specific reason, explicit confirmation, optimistic version agreement where applicable, and request-scoped audit evidence.
- Index maintenance is an explicit `ANALYZE` operation with recorded document counts, index sizes, duration, actor, reason, and bounded failure code. It does not claim to be a separate search-index rebuild.

## Consequences

- Newly eligible public content remains immediately searchable while common normalized prefix and substring filters gain targeted indexes.
- Ranking releases are measurable, attributable, reversible to zero exposure, conflict-safe, and visible in both public responses and Admin evidence.
- Offline evaluation uses deterministic seeded relevance cases rather than production behavioral telemetry. Richer signals, automatic rollback thresholds, and production experiment monitoring require separately governed data and remain future work.
- A failed candidate remains preserved as evidence but cannot receive traffic until a passing evaluation exists for the exact candidate and baseline pair.
