# ADR 0017: Immutable transaction risk rule revisions

## Status

Accepted on August 11, 2026.

## Decision

- Migration `0022_risk_rule_revisions` stores immutable, globally ordered rule revisions and an optimistic singleton pointer. Only administrators with `admin:risk_rules` may read or activate revisions.
- The initial revision preserves verified behavior exactly: task disputes score 85, Local Test refunds score 55, and medium/high/critical thresholds are 40/70/90.
- A new revision requires a bounded name, specific reason, explicit confirmation, the exact active version, scores from 0 through 100, and strictly ordered medium/high/critical thresholds. Activation appends a child revision, advances the pointer, and records request-scoped `admin.risk_rules_updated` evidence in one transaction.
- Risk producers load the active revision inside their existing business transaction. The stored signal includes its derived score and severity plus the exact `riskRuleRevisionId` and `riskRuleVersion` in bounded evidence.
- Existing signals are never recalculated when rules change. Their original classification and rule reference remain historical evidence; only subsequent signals use a newly activated revision.
- Revisions reject update and delete operations at the database boundary. The migration has a complete structural rollback.

## Consequences

- Operators can tune task-dispute and Local Test refund sensitivity without deploying code, while stale browser state and malformed threshold ordering fail closed.
- A future review can reproduce which rule classified any signal instead of interpreting it through current settings.
- The current rule set is intentionally global and limited to two verified producers. Broader signal types, per-market policies, evaluation gates, and production escalation automation require separately governed revisions.
