# ADR 0028: Default-off OpenAI Chat and Image runtime

Date: August 18, 2026

## Status

Accepted

## Context

ADR 0027 introduced an exact-capability Provider runtime boundary, but only the deterministic `local_test` implementation existed. The next production-facing step is a real vendor adapter that can be verified without silently authorizing credentials, paid calls, or traffic. Chat and Image are the first two modes because the official synchronous APIs fit the existing durable job contract; Video and Music require separate Provider selection and lifecycle acceptance.

## Decision

- Implement OpenAI Chat with `POST /v1/responses`, `store=false`, bounded output tokens, and aggregation of every `output_text` content part.
- Implement OpenAI Image with `POST /v1/images/generations`, one base64 PNG result, reviewed size/quality, bounded response and decoded sizes, and actual PNG dimension validation.
- Use the Go standard HTTP client and request context. Route revision deadlines remain the authoritative timeout boundary.
- Register one OpenAI runtime supporting only the configured exact Chat and Image model identifiers.
- Require `OPENAI_ENABLED=true`, `OPENAI_PAID_CALLS_APPROVED=true`, and an API key before registration. Production accepts only `https://api.openai.com/v1`; development plain HTTP is limited to loopback contract tests.
- Keep migration `0036` Provider Profiles disabled. Runtime registration, Admin Provider enablement, and immutable model-route activation are three independent gates.
- Treat authentication, content rejection, invalid requests, and malformed successful output as terminal. Treat rate limits, timeouts, network failures, and server unavailability as retryable. Bound `Retry-After` to fifteen minutes.
- Never expose or persist upstream response bodies, messages, request IDs, credentials, organization identifiers, or project identifiers.
- Keep HCAI billing amounts as explicit Local Test reservation estimates. Actual Provider invoice reconciliation remains a release requirement.

## Consequences

- The complete OpenAI request/response and failure contract can be tested against local `httptest` servers without credentials or paid calls.
- API and worker processes build the same runtime catalog from validated configuration, preventing Admin readiness from disagreeing with worker execution capability.
- Adding credentials alone cannot produce traffic. Conversely, an enabled database profile cannot serve traffic when process configuration is absent.
- OpenAI Video/Music, real-cost reconciliation, Provider deletion propagation, staging canaries, and production content-policy operations remain external acceptance work.
