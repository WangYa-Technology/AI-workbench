# ADR 0030: Explicit trusted proxy boundary for network evidence

## Status

Accepted for local and staging implementation; production deployment acceptance remains external.

## Decision

The API accepts `X-Forwarded-For` for privacy-minimized account-link evidence only when the direct TCP peer belongs to a configured, non-zero `TRUSTED_PROXY_CIDRS` prefix. Values are parsed as IP addresses; malformed values are ignored and the direct peer is used. With no configured prefix, forwarded headers are ignored.

## Rationale

Unconditionally trusting forwarding headers lets clients choose the network evidence attached to an account-link event. Explicit CIDR trust keeps the local boundary fail-closed while supporting a known deployment proxy. The application hashes the selected address immediately, so this setting does not authorize raw IP retention.

## Operational acceptance

The deployment proxy must sanitize and replace forwarding headers, and operators must verify proxy network ranges, retention, privacy notice/legal basis, logging, and incident response before production activation.
