# ADR 0032: S3-compatible media storage and scanner boundary

## Status

Accepted on August 18, 2026.

## Decision

Every byte-backed generated or uploaded Asset records an immutable `storage_backend` and `storage_key`. API and Worker processes construct the same catalog from configuration. Development defaults to an atomic create-only local-file Store and deterministic scanner. Production fails before database or network access unless a private S3-compatible Store and authenticated HTTPS scanner are configured.

The S3 adapter uses SigV4, SHA-256 checksums, `If-None-Match: *`, AES-256 server-side encryption, bounded retries, optional path-style addressing, and a safe object prefix. Asset bytes are never exposed by a bucket URL. The HTTP API resolves the recorded Store only after existing ownership, entitlement, publication, and clean-scan checks, then streams either the full object or one validated byte range.

Uploads now authorize the active account and any base version inside the account lifecycle transaction before writing bytes. A lost commit acknowledgement triggers serialized identity verification; committed originals are preserved, and failed verification never authorizes deletion. Known uncommitted writes use bounded best-effort cleanup. A durable pre-write upload journal and crash/late-write cleanup remain incomplete (see the upload correction below). Generation outputs use a deterministic Asset key; an existing object is reusable only when its complete bounded body exactly matches the Provider output. Account deletion resolves and removes each owned upload or generation through its recorded backend while retaining only the existing minimized database receipt.

The scanner receives the raw bounded object body with Bearer authentication, MIME type, object key, contract version, and SHA-256 headers. Its closed JSON response must echo the digest and contain only bounded status, reason, engine, and version tokens. Raw scanner responses, credentials, endpoint details, and upstream request identifiers are never persisted.

## Storage absence correction (2026-09-21)

The storage absence correction on 2026-09-21 distinguishes an inaccessible local root or S3 bucket from a missing object. S3 404 responses require an uncached signed `HeadBucket` before returning object absence; deployment credentials must support that operation on the configured bucket. Cleanup fails without recording completion when the root/bucket is unavailable, and can resume once the original location is restored. This checks the configured primary location only, not historical bucket identity, versions, backups or external restores. Scope and verification are recorded in [the marketplace audit](../resource-marketplace-flows.md#11113-存储位置不可用不能作为文件删除证明).

The subsequent local-directory correction binds each Store to its observed filesystem directory identity. Existing roots are captured at construction; missing development roots may initialize on first use. An observed root cannot be recreated by later writes, and replacement directories are rejected before object operations. Restoring the original directory permits recovery without changing the Store or cleanup job. This is an instance-lifetime guard, not durable evidence across restart or reconstruction after the replacement; [verification and remaining boundaries](../resource-marketplace-flows.md#11114-本地目录替换与写入自动重建的保护) remain explicit.

## Scanner boundary correction (2026-09-20)

HTTP scanning never follows redirects, including same-origin redirects. Verdicts
must be complete and at most 16 KiB after decompression; the client reads one byte
past the limit before parsing, so a valid JSON prefix cannot conceal a second
verdict or trailing data. The timeout includes the full response body. Transport
interruptions remain retryable; permanent scanner failures complete the asset as
`review` without granting clean status, and exhausted transient failures retain
the same controlled-review path. Scan-completion notices describe scanning
without claiming every adapter is the local deterministic scanner.

These changes were verified with loopback servers and isolated schemas. They do
not prove real scanner efficacy, production endpoint isolation or private-bucket
policy. See [marketplace rules](../resource-marketplace-flows.md#626-扫描服务的请求响应与失败收尾).

## Scan execution recovery (0121)

Upload, queue insertion and immutable scan binding commit together. Scanning checks the current durable lease and attempt before external work, then locks the asset and job and rechecks database wall time before saving a verdict. It does not hold job locks during scanner/storage calls. A minutely bounded recovery pass moves only evidenced failed original executions from pending to review; it preserves manual review and queued retries. Failure rolls back state, notification and audit together and records a bounded backoff. Deleted accounts receive no recreated scan notification.

Historical bindings are backfilled only when unambiguous. Stop old writers and drain running jobs before migration; old applications are rejected, and bindings prevent unsafe rollback. Running environments have not been upgraded. See [scope and tests](../resource-marketplace-flows.md#627-扫描执行绑定租约复核与中断恢复0121).

## Upload commit acknowledgement correction

The upload transaction holds the account lifecycle lock and a shared account row lock through Put and commit. Inactive accounts and unauthorized versions are rejected before storage access. On commit acknowledgement loss, a fresh transaction reacquires the lifecycle lock and verifies this invocation's asset ID, owner, upload source and storage identity. A matching committed asset is returned without another insert/scan; verification failure preserves bytes. Verification and rollback cleanup each have a five-second bound. HTTP upload returns 403 when authorization is revoked after initial authentication.

This prevents destructive cleanup after a successful commit; it is not a complete upload journal. Process crashes before metadata commit, ambiguous Put results, failed cleanup and retry idempotency still need durable intent tracking plus retention/deletion/export integration. No new migration or running deployment was performed. See [scope and tests](../resource-marketplace-flows.md#630-商品来源上传的提交响应丢失与账户状态复核).

## Consequences

- Production containers no longer share a writable media volume.
- Historical local objects remain readable after switching the primary Store because the catalog registers both S3 and local backends.
- Storage/scanner outages retain durable retry and controlled-review behavior without weakening clean-only access.
- Real service credentials, private-bucket policy, lifecycle and backup rules, malware/content-safety efficacy, deletion behavior, and staging isolation remain external acceptance gates.

### Upload write ownership before external I/O (0122)

Uploaded originals now have an immutable `upload_writes` intent committed before Put. Each invocation owns a unique reserved location and asset ID; conflicting bytes cannot be adopted. Asset creation, scan execution binding, audit and attachment commit atomically. Lost registration acknowledgements stop before storage I/O; lost final acknowledgements retain the existing serialized asset check. Request failure never unconditionally deletes an original.

Unbound cleanup verifies holds, all relevant file references, digest/size and post-Delete absence. Busy records are skipped; owner-lock deferral occurs under the same intent lock so concurrent janitors cannot postpone each other's active work. Interrupted checks record bounded nonblocking backoff. Verified-absent tombstones remain periodically eligible for late writes. Account deletion includes these records before completion, and owner exports omit locators and hashes. Aggregate failure/due/age gauges complement maintenance health.

Migration 0122 protects the reserved namespace, immutable identity, one intent per asset, binding completeness and writer/worker protocol. Drain old traffic, workers and external writes; evidence blocks downgrade. Isolated tests cover actual child-process exit, ambiguous writes/commits, false delete acknowledgements, concurrent janitors, retention, export/deletion and protocol changes. This does not implement client upload idempotency or inventory pre-journal/backup objects and is not production acceptance. See [the detailed contract](../resource-marketplace-flows.md#631-上传写入日志与未绑定原件清理0122).

### Account-scoped upload commands and independent attempts (0123)

An upload retry is now identified by a required HTTP idempotency key scoped to the active account, with a versioned hash of normalized title/filename, actual file digest/size/type, base asset and version note. Completed commands return the original asset's current state without another storage write, scan or version event. Changed requests conflict. A command may have multiple immutable failed attempts, each with its own 0122 location; it has at most one immutable successful asset result. Registration precedes storage, and a lost registration response stops I/O. Result completion commits with the asset, scan binding, audit and journal attachment. Concurrent calls recheck completion under account/command locks before writing.

Using a fresh attempt after failure avoids reviving a cleaned location or letting a late remote Put overwrite the final attempt. Old attempts retain their cleanup obligations. The database requires command result evidence for new reserved upload locations and rejects old writer/worker protocols; historical records are not assigned invented keys. Minimal owner export projections omit keys and request hashes. Account deletion retains minimal command associations and continues to clean unattached writes through 0122.

The browser snapshots forms, hashes file bytes, and shares the existing account-scoped command retry implementation across upload callers. It stores opaque keys/hash indexes only, preserves uncertain results through same-tab refresh, and isolates login epochs so old callbacks cannot erase new pending operations. Intentional new keys remain new operations; cross-tab or cleared-storage deduplication is not inferred from identical content. Upload and command recovery tests use isolated databases, storage and browser services. Real-storage/backup inventory, production capacity and full marketplace acceptance remain separate requirements; see [the 0123 contract](../resource-marketplace-flows.md#632-上传命令幂等恢复与客户端重试0123).
