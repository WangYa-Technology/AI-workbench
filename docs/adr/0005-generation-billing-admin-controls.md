# ADR 0005: Transactional generation billing and controlled Admin operations

## Status

Accepted on August 11, 2026.

## Decision

- Every Local Test generation reserves its estimated cost in the same transaction that creates the generation and durable job.
- A successful worker transaction creates the typed Asset and captures the reservation exactly once. Cancellation or final failure releases it; retries create a new generation, reservation, and explicit lineage.
- Chat, Image, Video, and Music share the same generation state machine and Provider profile boundary while producing mode-specific text, JPEG, MP4, and WAV results.
- Billing entries are immutable evidence. Available balance is derived from balance minus active reservations, and Admin adjustments append entries instead of rewriting history.
- Admin mutations enforce persisted permissions and require a reason plus explicit confirmation for controlled operations. Every accepted mutation writes audit evidence with actor, target, request ID, and structured details.
- Real model calls and payment activity remain fail closed until approved Provider/payment adapters and credentials exist.

## Consequences

- Generation submission cannot succeed without enough Local Test credit, and users cannot spend reserved credit twice.
- Worker replay, command replay, cancellation races, and final failure cannot capture a generation more than once or strand a reservation.
- Operational staff can inspect and intervene without receiving unrestricted database access, while users retain a readable personal statement.
- Local outputs verify product workflows and media contracts; they do not claim commercial model quality or real financial settlement.
