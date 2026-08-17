# ADR 0021: Signed Webhook outbox and recovery

## Status

Accepted on August 11, 2026.

## Decision

- Signed Webhooks are separate from Developer API keys. Owners create up to five endpoint subscriptions under the default-off Developer Access control; outbound signing secrets never authenticate inbound product or Developer API requests.
- Each endpoint secret is a random `whsec_...` credential returned once after creation or rotation. PostgreSQL stores an AES-256-GCM ciphertext revision with endpoint/version associated data. Production requires an explicit 32-byte `WEBHOOK_ENCRYPTION_KEY_B64`; non-production may use the documented deterministic local fallback.
- Requests sign `timestamp.rawBody` with HMAC-SHA256 and send `HCAI-Webhook-Id`, `HCAI-Webhook-Timestamp`, and `HCAI-Webhook-Signature: v1=<hex>`. A delivery retains the secret revision selected when it was queued; post-rotation events use the new revision.
- Business transactions append closed-catalog events and delivery jobs to a PostgreSQL outbox. The catalog is `developer.webhook.test`, `generation.completed`, `work.published`, `marketplace.order.fulfilled`, and `marketplace.order.refunded`.
- Network failures, `408`, `409`, `425`, `429`, and `5xx` retry with bounded delays for at most five attempts. Other `4xx` responses enter dead letter immediately. Delivery attempts are append-only and retain status, duration, error classification, and response SHA-256 only; response bodies are never persisted.
- Production accepts only HTTPS public targets. Development additionally permits loopback HTTP for deterministic tests. URL credentials, fragments, queries, redirects, private/link-local destinations, and address changes that resolve outside the validated boundary fail closed.
- Administrator replay requires `admin:developer`, a bounded reason, explicit confirmation, exact delivery version, audit evidence, owner notification, and a new delivery linked to the original dead letter. Replay signs with the endpoint's current active secret revision.
- Account export includes owner endpoint configuration plus event/delivery/attempt evidence but omits plaintext secrets, nonces, and ciphertext. Account deletion revokes endpoints, cancels pending work, anonymizes endpoint identity, minimizes event payloads, and erases encrypted secret revisions while retaining bounded delivery evidence. Migration `0028_webhook_data_rights` is structurally reversible only before that erasure executes and fails explicitly afterward.

## Consequences

- Webhook delivery is durable across API/worker restarts and exposes actionable, privacy-bounded recovery evidence.
- Receivers must preserve raw request bytes, enforce a timestamp tolerance, compare signatures in constant time, and support overlap planning before an owner rotates a secret.
- Local loopback verification proves protocol and state-machine behavior but does not approve a production endpoint, DNS policy, encryption-key custody, egress controls, or incident runbook.
