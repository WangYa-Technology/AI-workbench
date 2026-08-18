# ADR 0029: Default-off Stripe payment boundary

Date: August 18, 2026

Status: accepted for local implementation; merchant activation requires external acceptance.

## Context

HCAI CHAT needs product licensing and task funding without accepting card data or treating local credits as real money. Payment confirmation, refunds, creator transfers, and operational recovery must survive process restarts and duplicate Provider delivery. No workspace change grants authority to create real charges.

## Decision

- Keep the existing balanced Local Test USD ledger as the default development workflow.
- Add a default-off `payments.ProviderRuntime`; register Stripe only after strict process configuration validates credentials, endpoint, API version, Webhook tolerance, test/live mode, and separate live-mode approval.
- Use Stripe-hosted Checkout for product and task funding. HCAI accepts no card data and persists only internal resource links plus bounded Stripe identifiers.
- Treat signed Provider events as the source of payment confirmation. Verify exact raw bytes and timestamp, pin API version and mode, hash the payload, minimize persisted fields, and reject changed payloads for an existing event ID.
- Process entitlement, task assignment, refunds, and transfers through durable fenced jobs. Provider idempotency keys derive from immutable internal operation IDs.
- Require a verified payout destination with charges and payouts capabilities before a creator transfer can succeed.
- Expose payment attention, recovery, event replay, and destination changes only through permission-scoped Admin operations requiring reason, confirmation, expected version, and audit evidence.

## Consequences

- Product and task flows remain functional without Stripe through explicit Local Test commands; the UI and API can also expose hosted checkout when the runtime is enabled.
- Duplicate events and worker retries do not create duplicate local transitions or Provider operations.
- Raw event bodies, payment credentials, card data, and upstream error prose do not enter business records, API errors, logs, or Admin projections.
- Merchant onboarding, registered Webhook deployment, Connect/KYC, payout policy, disputes, tax, invoices, reconciliation, and live-mode approval remain external release conditions.

## Verification

- Offline Stripe HTTP contracts cover checkout, refunds, transfers, bounded responses, idempotency headers, safe failure classes, timeout, and rate limits.
- PostgreSQL and HTTP contracts cover signed Webhook acceptance, duplicate/conflict/version/mode rejection, product/task state transitions, entitlement/assignment, recovery/replay, payout destinations, authorization, optimistic versions, and cursors.
- Migrations `0037` and `0038` apply to the development database with paired rollback files.
- Frontend lint/typecheck, complete HTTP package tests, and full isolated Chromium `51/51` pass with Stripe disabled and no external call.
- `make payment-drill` runs the API, worker, and a loopback fixture in an isolated schema and proves Checkout/Webhook/refund idempotency, durable entitlement grant/revocation, and Admin evidence without contacting Stripe.
