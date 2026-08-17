# ADR 0014: Locale contract and stable error localization

## Status

Accepted on August 11, 2026.

## Decision

- English (`en-US`) remains the source locale and default product language. Simplified Chinese (`zh-CN`) must expose the same message keys and interpolation parameters, with no empty values.
- User-facing static Vue template copy belongs in the locale catalog. An AST-based unit test inspects every Vue SFC and rejects visible text or accessibility attributes that bypass vue-i18n.
- API errors are localized from stable error codes in the client. Server prose is never used as user-facing fallback copy; unknown codes fail closed to a localized generic or retryable message, while request IDs remain visible for support correlation.
- The locale contract scans Go HTTP transports and requires a translation for every stable code passed to `WriteError`.
- Admin system values are localized by explicit domain maps. Statuses are interpreted in their owning lifecycle because identical values can have different meanings across users, orders, tasks, risks, and Providers. Unknown enum values display a localized unknown-state label instead of leaking internal identifiers.
- Dates, numbers, currencies, and time zones continue through locale-aware formatting helpers. User-authored content, immutable audit actions, model identifiers, provider identifiers, filenames, MIME types, hashes, and evidence reasons remain verbatim.
- Playwright verifies the Chinese core workflow at mobile and desktop widths, checks document overflow and raw message keys, inspects Admin enum rendering, and proves that stable API failures do not expose English server copy.

## Consequences

- Adding a source message, interpolation parameter, or stable API error code without the matching Chinese contract fails the test suite immediately.
- Backend message wording can change without silently changing localized product copy.
- New lifecycle enums require an intentional user-facing label before they can appear cleanly in Admin.
- The automated locale gate does not replace production linguistic and legal review. Jurisdiction-specific legal copy remains an external release-acceptance boundary.
