# ADR 0013: Accessible navigation and continuous WCAG gate

## Status

Accepted on August 11, 2026.

## Decision

- The application shell exposes a localized skip link as the first keyboard stop. The main landmark is programmatically focusable without entering the normal tab order.
- Client-side path changes move focus to the main landmark after rendering and announce the new `h1`. Initial route resolution does not steal focus, and query-only filter changes do not reset the user's position.
- Desktop and mobile navigation expose localized accessible names and `aria-current="page"` on active destinations.
- Text assets retain bounded scrolling and are keyboard focusable. Light/dark semantic tokens and status treatments must meet normal-text contrast rather than relying on larger display text exemptions.
- Playwright runs axe-core against Discover, Create, Assets, Publish, Marketplace, Tasks, Community, Search, Account, and Admin in both light and dark themes using `wcag2a`, `wcag2aa`, `wcag21aa`, and `wcag22aa` tags.
- Axe failures report rule, impact, selector, and failure summary. The gate does not disable color contrast or accept known violations.
- A separate keyboard path verifies skip-link activation, route focus, active-page evidence, and operability with reduced motion.

## Consequences

- Keyboard and screen-reader users receive predictable entry and navigation behavior despite Vue route transitions.
- Color and small-status changes can no longer silently regress the critical routes covered by the automated gate.
- Automated rules do not replace assistive-technology review. Production acceptance still requires representative manual screen-reader testing for supported browser and operating-system combinations.
