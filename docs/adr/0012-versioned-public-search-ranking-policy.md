# ADR 0012: Versioned public search ranking policy

## Status

Accepted on August 11, 2026.

## Decision

- Public search relevance weights live in PostgreSQL as immutable revisions. A singleton state row points to the active revision; activating a policy appends a new revision rather than overwriting evidence.
- The initial revision exactly preserves the previously verified SQL weights for title exact/prefix/contains matches, creator evidence, primary/secondary body evidence, recency, creator activity, and result-type boosts.
- Every public search request reads the active revision and passes its integer weights as bound SQL parameters. Search responses expose the policy name and version alongside per-result rank signals.
- Only `admin:ranking` may activate a revision. The command requires a bounded name, ordered weights, type-boost limits, a specific reason, explicit confirmation, and the expected active version.
- Activation locks the singleton state, creates a child revision, advances the pointer, and writes `admin.discovery_ranking_updated` audit evidence in one transaction. Revision rows reject updates and deletes.
- The Admin UI exposes the active values and the latest 20 immutable revisions in en-US and zh-CN. It does not claim that manual weights are a recommendation model or a substitute for evaluation.

## Consequences

- Ranking changes are attributable, conflict-safe, reproducible, and visible in the public response that used them.
- Rollback is performed by activating a new revision with prior values, preserving the full decision history instead of mutating the pointer without evidence.
- The search query remains a live relational query. Production-scale indexing, offline evaluation, staged rollout, automatic rollback thresholds, and richer behavioral signals remain separate work and cannot be implied by this control surface.
