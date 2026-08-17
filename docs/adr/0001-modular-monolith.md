# ADR 0001: Go modular monolith with Vue client

- Status: accepted
- Date: 2026-08-10

## Context

The source product contains many operational models and cross-domain contracts, but the requested target is a new Go and Vue implementation. Splitting those contracts across services before the product workflows stabilize would add deployment and consistency costs without improving the user goal.

## Decision

Use a Go modular monolith with:

- domain packages that own state and invariants;
- application services that coordinate transactions;
- PostgreSQL repositories as the business source of truth;
- HTTP transport and OpenAPI at the system boundary;
- Provider adapters behind capability interfaces;
- a separate worker process that claims durable PostgreSQL jobs;
- one Vue 3 client generated against the API schema.

The API and worker are separate binaries but share domain and repository packages. Modules may publish durable outbox events without requiring a network service boundary.

## Consequences

- Cross-domain workflows can use database transactions and explicit service calls.
- Deployment remains understandable for local development and an initial production environment.
- Packages must enforce dependency direction to avoid recreating the source repository's broad central wiring.
- A future service extraction requires measured scaling, isolation, or ownership evidence and a new ADR.
