# ADR 0026: Privacy-minimized registration account-link risk

## Status

Accepted on August 11, 2026.

## Context

The cross-domain risk queue covered disputes, refunds, Community reports, and rejected media, but it could not identify a burst of distinct registrations sharing one network boundary. A raw IP address, device fingerprint, reusable network identifier, or linked-account list in risk evidence would create unnecessary identity data and broaden Admin exposure. Login activity and shared networks also must not become automatic findings of abuse.

## Decision

- Only account registration evaluates this signal. Ordinary login and existing-session activity never create or refresh account-link evidence.
- The HTTP boundary transforms the request network address with the existing domain-separated SHA-256 function before the session transaction. PostgreSQL receives no raw address for this workflow.
- After the new session is inserted, the registration transaction counts distinct active accounts with the same non-null network hash inside the active immutable risk rule's bounded window. The default boundary is the third distinct account within 24 hours.
- A threshold crossing creates one idempotent `user/account_link` risk signal for the newly registered account. Its stable source key is scoped to that account; replay returns the same signal.
- The signal stores only the aggregate linked-account count, configured minimum, configured window, `networkDataStored: false`, score, severity, and exact immutable risk-rule revision ID/version. It stores no raw IP address, network hash, device fingerprint, user-agent fingerprint, or linked-account identifiers.
- The default score is 65, which classifies as medium under the existing 40/70/90 thresholds. Administrators can change the score, minimum count, and 1-168 hour window only through a confirmed, attributable, immutable risk-rule revision.
- Risk evidence is a review lead, not proof of common control or misconduct. It creates no automatic account restriction and remains behind `admin:risk`; review still requires reason, confirmation, expected-version agreement, append-only events, and audit evidence.
- Admin resolves the signal to the subject account and a stable Users-tab search. Exact `resourceType=user` plus `resourceId` focus is applied before pagination.
- An owner export includes the bounded signal and evidence because it is account-related personal-data processing. The export omits session/network values and other linked identities. Account deletion clears every retained `sessions.network_hash` for the subject while preserving only the existing bounded safety/audit evidence.
- Migration `0034_identity_link_risk` extends the closed resource/signal catalogs and immutable rule schema. Rollback fails explicitly once account-link evidence exists rather than deleting review history.

## Consequences

- Operations gains a deterministic local abuse-rate lead without persisting a reusable network identifier in the risk queue.
- Shared households, schools, offices, VPN exits, and carrier networks can cross the threshold, so the signal cannot justify automatic enforcement.
- A network hash remains temporarily present in session records to perform the bounded comparison. Production retention duration, proxy trust configuration, privacy notice, legal basis, and regional applicability require explicit legal and security acceptance before launch.
- Cross-device identity resolution, browser fingerprinting, graph inference, external fraud vendors, and production automated enforcement remain outside this decision and fail closed.

## Verification

- Identity tests prove registrations one and two create no signal, registration three creates exactly one medium-score signal under rule version 1, ordinary login does not duplicate it, and a different network does not trigger it.
- Admin and HTTP tests prove immutable account-link rule fields, member denial, exact user-resource focus, safe evidence, and stable account deep links.
- Data-rights tests prove the owner export contains bounded account-link evidence without network identity and account deletion nulls every subject session network hash.
