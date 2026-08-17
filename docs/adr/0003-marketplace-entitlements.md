# ADR 0003: Marketplace entitlements and immutable Local Test order evidence

## Status

Accepted for CP-04.

## Context

A digital-product purchase grants usage rights rather than transferring authorship. The product catalog may change after purchase, while the buyer must still be able to prove the exact product, license version, terms, refund window, seller, and source Asset accepted at checkout. Browser state or a mutable catalog row cannot provide that evidence.

## Decision

- PostgreSQL is the source of truth for products, licenses, orders, ordered events, entitlements, purchased Assets, audit records, and ledger entries.
- Checkout requires an authenticated buyer, an active clean product, explicit license acceptance, and an idempotency key. Sellers cannot purchase their own products.
- An order snapshots product title, license name, version, terms, and refund-window days. Catalog edits never rewrite prior order evidence.
- Local Test checkout records `realCharge=false`, creates one debit and one credit, grants one active entitlement, and creates one buyer-owned Asset linked to the original Asset.
- A purchased source Asset cannot be published unchanged. It may enter Create only while its entitlement is active and its license allows derivatives.
- Generated derivatives preserve `source_asset_id`; Asset provenance traverses the generation, purchased source Asset, order, product, seller, and license evidence.
- Refund uses the snapshotted window, records ordered request/completion events, revokes the entitlement, removes the purchase from usable Assets, and writes a separate balanced reversal operation.
- Order events carry an order-local monotonic sequence. Equal timestamps and random UUIDs do not determine business order.

## Consequences

- The local workflow can prove rights and provenance across purchase, creation, and refund without claiming a real payment occurred.
- Production payment authorization, tax, payout, chargeback, fraud, invoicing, and legal review remain separate external acceptance boundaries.
- License updates require a new versioned license code for future products rather than mutation of evidence already accepted by buyers.
