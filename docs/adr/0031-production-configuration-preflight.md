# ADR 0031: Fail-closed production configuration preflight

## Status

Accepted on August 18, 2026.

## Decision

Production API, Worker, migration, seed, and configuration-check processes use the same `config.Load` boundary. Before any process opens PostgreSQL or calls a Provider, production mode requires a complete PostgreSQL URL with `sslmode=verify-full`, a public path-free HTTPS `WEB_ORIGIN`, secure Cookie and Webhook settings, disabled local Provider and email adapters, replaced deployment placeholders, and distinct 32-byte keys for Webhook and identity-action encryption.

`cmd/configcheck -require-production` exposes this boundary as a deployment preflight. It performs no network operation and returns only safe environment/mode booleans and a trusted-proxy prefix count. It never returns database locations, key material, or Provider credentials.

## Consequences

- A production process fails before migration or traffic when transport, origin, local-adapter, placeholder, or encryption-domain isolation requirements are unsafe.
- Operators can validate secret-manager injection independently of database availability and container startup.
- The preflight does not prove database reachability, proxy sanitization, secret custody, external Provider acceptance, object storage, backup scheduling, monitoring retention, or legal readiness; those remain separate release gates.
