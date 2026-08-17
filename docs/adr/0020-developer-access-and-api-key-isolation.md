# ADR 0020: Developer Access and API key isolation

## Status

Accepted on August 11, 2026.

## Decision

- Migration `0026_developer_access` adds a default-off global control, owner-scoped Service Accounts, independently revocable API keys, bounded expiry, a closed scope catalog, normalized IP CIDR allowlists, optimistic versions, usage evidence, and complete structural rollback.
- API keys use `hcai_sk_<public-prefix>_<secret>`. Plaintext is returned exactly once after issue or rotation; PostgreSQL stores only the public prefix, display hint, and SHA-256 secret hash. Logs, audit metadata, Admin inventory, exports, and API projections must never contain plaintext or the secret hash.
- Developer Bearer authentication is accepted only by the explicit `/api/v1` Developer contract. Existing product APIs remain Cookie-only and reject Bearer-only access, preventing machine credentials from inheriting browser capabilities.
- The initial scope catalog is deliberately closed to `developer:identity:read`. Unknown scopes, invalid CIDRs, expired credentials, revoked accounts, disabled global access, disallowed networks, and malformed keys fail closed.
- Owners can create and revoke Service Accounts and issue, rotate, or revoke keys within administrator-defined limits. Rotation atomically invalidates the previous key. Administrator emergency revocation requires `admin:developer`, a bounded reason, explicit confirmation, the exact current version, and immutable audit evidence.
- OpenAPI, generated Vue types, en-US/zh-CN Account and Admin surfaces, domain/HTTP tests, accessibility coverage, and the browser lifecycle share this boundary.
- Signed webhooks are a separate checkpoint. Webhook signing secrets, durable deliveries, retries, replay, and dead-letter behavior do not reuse API-key records because inbound authentication and outbound signing have different ownership and lifecycle risks.

## Consequences

- A database read cannot recover an API key, and a leaked key can be revoked without changing the owner's password or browser sessions.
- Service Accounts stay personal and bounded in this checkpoint. Team ownership, broad write scopes, OAuth clients, and delegated user authorization require separate design and security review.
- CIDR allowlists reduce exposure but are not a substitute for TLS, secret management, rotation, rate limiting, or production network controls.
- Production rollout remains fail closed until operators explicitly enable Developer Access and accept secret-handling, abuse monitoring, and incident-response procedures.
