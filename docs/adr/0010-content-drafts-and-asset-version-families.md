# ADR 0010: Persisted content drafts and immutable Asset version families

## Status

Accepted on August 11, 2026.

## Decision

- Content drafts use the existing PostgreSQL Work and Post aggregates with an explicit `draft` state. Draft content is never treated as browser-local state, and owner-scoped create, read, list, update, discard, and publish commands share the public publishing authorization boundary.
- Work and Post rows carry optimistic versions. Updates, discard, and publish require the expected version so concurrent browser sessions fail with a conflict instead of silently overwriting newer content.
- One owner can hold at most one active draft for an Asset. Drafts may be incomplete while private; publication revalidates title, AI disclosure, Asset ownership, clean scan state, and the prohibition on republishing purchased source files unchanged.
- An Asset version is a new Asset row with new media bytes, a stable `family_id`, an increasing family-local version number, a predecessor reference, and a bounded change note. Uploading a version never overwrites or reuses the previous file.
- Every uploaded version starts at `pending` and enters the durable scan workflow independently. Earlier versions retain their state and provenance. Workspace inventory collapses each family to its latest version while Asset detail exposes the complete ordered history.
- Purchased source Assets cannot create versions because their licensed original must remain byte- and evidence-stable. Other owned Asset origins can create a same-kind version through the upload boundary.
- `asset_version_events` is append-only. Data export includes actor-owned version evidence; account deletion clears Asset change notes and replaces event reasons with one fixed redaction marker through a transaction-local maintenance setting that cannot alter structural fields.

## Consequences

- A user can leave Publish, refresh, or reopen another browser session without losing draft state, and stale tabs cannot overwrite or publish newer edits.
- Asset evolution remains auditable: v2 is a separately scanned object linked to v1 rather than a mutable filename or hidden replacement.
- Listing only the latest family member keeps the workspace usable, while direct detail routes preserve old versions for lineage and evidence inspection.
- Production object storage and external scanners must preserve family identity, predecessor links, independent scan states, and append-only evidence when replacing the local adapters.
