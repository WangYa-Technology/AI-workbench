# ADR 0007: Asset upload and fail-closed scan gate

## Status

Accepted on August 11, 2026.

## Decision

- User uploads pass through a bounded multipart endpoint. The server detects content type from bytes, accepts JPEG, PNG, MP4, WAV, and plain text, limits files to 10 MiB, and never trusts a browser-supplied MIME type or filename as a storage path.
- The upload transaction creates an owned Asset in `pending` state, enqueues a durable `asset.scan` job, and records audit evidence. File bytes are stored through the current local media boundary using generated identifiers.
- The local scanner deterministically checks the stored signature and explicit Local Test policy markers, producing `clean`, `review`, or `rejected`. Worker retries use the existing durable lease and attempt model.
- Asset content lookup requires `clean` status. Pending, review, and rejected bytes fail closed for owners and public viewers, which also prevents them from entering publish or reuse workflows.
- Manual review requires `admin:media`, a reason, and explicit confirmation. The same transaction changes status, notifies the owner, and appends Admin audit evidence with the previous and new states.
- Production object storage and commercial malware/content-safety scanners remain adapters outside this checkpoint. Production configuration must stay unavailable until credentials, retention policy, quarantine behavior, and scanner failure handling are approved.

## Consequences

- Uploading is a real asynchronous Asset workflow rather than a UI-only file picker, and scan state survives API or worker restarts.
- Content cannot become readable in the interval between upload and scanning, and a later Admin rejection immediately revokes content access without deleting evidence.
- The deterministic scanner validates lifecycle, permission, notification, and audit behavior locally but does not claim commercial malware or policy-detection coverage.
- Moving to S3-compatible storage or an external scanner must preserve the Asset state machine, clean-only access invariant, idempotent job behavior, and controlled-review evidence.
