# ADR 0023: Cross-domain governance risk signals

## Status

Accepted on August 11, 2026.

## Context

The first durable risk queue covered task disputes and Local Test refunds, but Community reports and uploaded-media rejection remained isolated in their domain queues. Operations therefore lacked one prioritized view across user safety and transaction risk. Copying free-form report or review text into another system would increase sensitive-data exposure and retention without improving classification.

## Decision

- Community report creation and uploaded-media rejection record risk signals in the same PostgreSQL transaction as the originating governance mutation.
- Source keys are stable and unique per report or Asset, so retries return the original signal instead of creating duplicate review work.
- The closed catalogs add `post/community_report` and `asset/media_rejection`. New types cannot enter persistence without a reviewed migration and corresponding contract/localization changes.
- Immutable risk-rule revisions add independent scores for Community reports and media rejection. Every signal stores the exact active revision ID/version and derives severity from the revision's ordered thresholds.
- Community signals include only the report ID and category. Media signals include only the prior scan state. Free-form report details and administrator reasons remain in their separately permissioned governance and audit records.
- Admin projections resolve a Community post to its stable Work details and an uploaded Asset to its owner-authorized Asset details. Risk review retains the existing permission, reason, confirmation, optimistic version, append-only event, and audit requirements.
- Account deletion retains bounded safety evidence under the existing `safety: retained_minimal` receipt disposition. Risk evidence is not added to the user export because it is internal abuse-prevention material and could expose operational review methods; ordinary authored content and governance requests remain covered by their existing export rules.
- Migration rollback is allowed only before Community/media risk evidence exists. Once such evidence is present, the down migration fails explicitly rather than deleting an audit trail.

## Consequences

- Operations receives a single comparable risk queue without weakening the original Community or media workflows.
- New signal sources use deterministic scores locally and can be tuned only through attributable immutable revisions.
- A report is a review signal, not a finding of wrongdoing. Its lower default score reflects that distinction; media rejection receives a higher score because it follows a controlled scan or administrator decision.
- Production escalation, automated abuse-rate aggregation, and account-linking remain separate work. This checkpoint does not infer identity relationships or claim automated enforcement.
