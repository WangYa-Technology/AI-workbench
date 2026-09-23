# ADR 0016: Controlled Admin task-dispute resolution

> 2026-09-18 implementation update: this ADR preserves the original local-ledger design. Current HTTP tasks require verified Provider funding. See [the current task lifecycle](../task-marketplace-flows.md) for participant privacy, delivery bundles and immutable grants, expiry/extension handling, asynchronous refunds/transfers, and the restored reason/confirmation/audit contract. The local settlement statements below are historical, not current payment behavior.

## Status

Accepted on August 11, 2026.

## Decision

- Task participants may open a dispute from a submitted or revision delivery, but only an actor with `admin:tasks` may resolve the resulting operational case. The permission is assigned to administrators, not ordinary members or moderators.
- Migration `0021_admin_task_operations` gives each dispute an optimistic version and makes ordered task events append-only. Existing disputes begin at version 1, and the migration has a complete down migration.
- The operations queue joins the task, commissioner, assigned creator, accepted proposal amount, latest delivery version, latest dispute, linked risk status, and Local Test settlement evidence. It does not expose credentials, payment details, or private resources outside the permission boundary.
- A controlled resolution requires the exact open-dispute version, a bounded specific reason, and explicit confirmation. Terminal disputes return a state conflict and cannot be overwritten.
- `release_creator` accepts the latest disputed delivery, marks the task accepted, records one Local Test settlement, performs one balanced billing transfer, resolves the dispute for the creator, notifies both participants, appends the task event, and writes Admin audit evidence in one transaction.
- `cancel_without_settlement` marks the task cancelled, resolves the dispute for the commissioner, creates no settlement or ledger movement, notifies both participants, appends the task event, and writes Admin audit evidence in one transaction.
- These decisions are Local Test accounting only. The API and UI must not imply real payment, escrow, legal adjudication, or production payout.

## Consequences

- Disputed tasks now have a complete operational terminal path instead of remaining indefinitely paused after risk detection.
- Participant notifications, task history, billing entries, settlement evidence, and Admin audit cannot disagree because they commit together.
- An insufficient Local Test balance prevents creator release without partially changing task or dispute state.
- Risk review remains a related but independent control. Resolving the commercial task state does not silently rewrite or close the append-only risk decision trail.
