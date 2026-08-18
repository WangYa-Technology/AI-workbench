# ADR 0032: S3-compatible media storage and scanner boundary

## Status

Accepted on August 18, 2026.

## Decision

Every byte-backed generated or uploaded Asset records an immutable `storage_backend` and `storage_key`. API and Worker processes construct the same catalog from configuration. Development defaults to an atomic create-only local-file Store and deterministic scanner. Production fails before database or network access unless a private S3-compatible Store and authenticated HTTPS scanner are configured.

The S3 adapter uses SigV4, SHA-256 checksums, `If-None-Match: *`, AES-256 server-side encryption, bounded retries, optional path-style addressing, and a safe object prefix. Asset bytes are never exposed by a bucket URL. The HTTP API resolves the recorded Store only after existing ownership, entitlement, publication, and clean-scan checks, then streams either the full object or one validated byte range.

Uploaded bytes are written before the database transaction and deleted when that transaction fails. Generation outputs use a deterministic Asset key; an existing object is reusable only when its complete bounded body exactly matches the Provider output. Account deletion resolves and removes each owned upload or generation through its recorded backend while retaining only the existing minimized database receipt.

The scanner receives the raw bounded object body with Bearer authentication, MIME type, object key, contract version, and SHA-256 headers. Its closed JSON response must echo the digest and contain only bounded status, reason, engine, and version tokens. Raw scanner responses, credentials, endpoint details, and upstream request identifiers are never persisted.

## Consequences

- Production containers no longer share a writable media volume.
- Historical local objects remain readable after switching the primary Store because the catalog registers both S3 and local backends.
- Storage/scanner outages retain durable retry and controlled-review behavior without weakening clean-only access.
- Real service credentials, private-bucket policy, lifecycle and backup rules, malware/content-safety efficacy, deletion behavior, and staging isolation remain external acceptance gates.
