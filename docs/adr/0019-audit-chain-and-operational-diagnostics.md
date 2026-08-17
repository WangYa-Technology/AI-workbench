# ADR 0019: Audit chain and operational diagnostics

## Status

Accepted on August 11, 2026.

## Decision

- Migration `0025_audit_observability` backfills every existing audit event into one continuous SHA-256 chain ordered by creation time and ID. Each event stores an immutable sequence, previous hash, and event hash; a locked singleton head serializes subsequent appends.
- PostgreSQL triggers calculate chain evidence and reject normal audit-event updates or deletes. The Admin verifier recomputes every event hash and predecessor link and compares event count, final sequence, and final hash with the stored chain head.
- API middleware accepts only bounded safe external request IDs and replaces invalid values. Structured request logs and persistent observations carry the request ID, method, matched route template, status, response bytes, and duration without storing query strings or bodies.
- API observations are retained for seven days. The permission-scoped Admin diagnostics read a consistent snapshot containing a 15-minute request summary, durable job status/retry/expired-lease evidence, database readiness, and current audit-chain verification.
- `admin:observability` is granted only to the administrator role. The OpenAPI contract, generated Vue types, localized Diagnostics tab, domain/HTTP/middleware tests, and browser workflow use the same boundary.
- The migration provides complete structural rollback. Rollback removes derived chain and request-observation structures but cannot preserve those diagnostics in the older schema.

## Consequences

- Audit tampering or a divergent chain head is visible through recomputation instead of relying on row presence alone. Appending audit events is intentionally serialized at the database chain head.
- Operators can distinguish request failures, latency, durable queue backlog, retries, and expired leases from a single local control surface while retaining request IDs for support correlation.
- Observation persistence is best effort after the response path: a recording failure is logged and does not alter the completed business response. Business mutations remain protected by their own transactions and audit writes.
- This local control plane does not claim centralized log retention, distributed tracing, paging, incident management, or production SLO monitoring. Those require externally configured infrastructure and release acceptance.
