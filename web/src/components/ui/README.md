# HCAI Appica-compatible Vue components

This directory provides Vue 3 equivalents for all 70 components in the Appica React catalog. It does not install or wrap `@appica/ui-react`; the upstream package requires React 19 and Tailwind CSS 4. Components use HCAI's semantic CSS tokens from `tokens.css`, global component styles from `ui.css`, and motion tokens from `motion.css`.

Import components from the shared barrel:

```ts
import { UiButton, UiDatePicker, UiDataTable } from '@/components/ui'
```

## Catalog coverage

- Actions and inputs: Accordion, Autocomplete, Button, ButtonGroup, Calendar, Checkbox, Chip, Collapsible, ColorArea, ColorPicker, ColorSlider, ColorSwatch, ColorSwatchPicker, Combobox, CopyButton, DateField, DatePicker, Field, Form, Input, NumberField, OtpField, Radio, Select, Slider, Switch, Textarea, TimeField, Toggle, ToggleGroup.
- Data display and layout: Avatar, Badge, Card, Carousel, Countdown, DataTable, Kbd, Meter, Progress, Rating, ScrollArea, Separator, Sparkline, Table, Thumbnail.
- Decoration and effects: BackgroundPattern, BorderBeam, GradientGlow, TextAnimate.
- Menus and navigation: Breadcrumb, ContextMenu, DropdownMenu, Menubar, Navigation, NavigationMenu, Pagination, Tabs, Toc, Toolbar.
- Overlays: AlertDialog, Dialog, Drawer, Popover, PreviewCard, Tooltip.
- Status and feedback: Alert, Loader, Skeleton, Spinner, Toast.

HCAI also keeps several product-specific primitives that are outside the official catalog: `UiAvatarGroup`, `UiDataList`, `UiEmptyState`, `UiFieldError`, `UiFileInput`, `UiFormField`, `UiIconButton`, `UiLabel`, `UiNotificationBadge`, `UiPanel`, `UiSegmentedControl`, `UiSheet`, and `UiStatus`.

## Compatibility contract

- Controlled values use Vue `v-model` conventions (`modelValue` plus `update:modelValue`).
- Open surfaces use `open` plus `update:open` where external control is useful.
- Native elements provide the baseline keyboard and form behavior; composite widgets add roving focus, Escape dismissal, focus containment, and ARIA state as appropriate.
- Colors, spacing, radii, focus rings, dark mode, RTL logical properties, and reduced-motion behavior come from shared tokens rather than page-local styling.
- Component APIs are Vue-native equivalents, not source-compatible ports of React props or Base UI compound parts.


## Product form and filter composition

- Catalog toolbars use `UiFilterBar` with `UiFilterSearch` / `UiSelect`; split toolbars place tabs and controls in their named CSS slots.
- Filter choices use `UiToggle` or `UiToggleGroup` for selected states; pages may arrange them but must not redefine their borders, backgrounds, focus rings or selected surfaces.
- Labeled admin, billing and generation filters use `<UiFilterBar fields density="compact" layout="grid">`. The component owns labels, 36px control/action sizing, surfaces and neutral focus. Pages may set grid columns and responsive placement, but must not redefine trigger borders, backgrounds or focus rings.
- Account, authentication and support forms use `UiForm`, with `surface="default"` or `surface="muted"`. Labels and 42px controls are shared. The native fieldset retains disabled behavior and uses `display: contents` so page grids remain effective; structural child selectors must account for this fieldset. Outer spacing belongs to the page; `--ui-form-gap` controls internal spacing for drawers or domain layouts.
- `UiInput`, `UiTextarea` and `UiSelect` own enabled/disabled, hover, focus and invalid states. Do not reproduce these states in page CSS.

## Empty content

- Use `UiEmptyState` for empty catalog, workspace and management results. Supply a localized `title`, optional `message`, a decorative `icon` slot and `UiButton` elements in `actions`. Existing default-slot actions remain supported.
- Use `density="compact"` for short empty messages inside sections and narrow sidebars. Native table empty rows stay inside their tables; loading, errors, permission gates and creative canvas onboarding are separate states.
- Distinguish filtered results from a genuinely empty collection before selecting copy. Offer clearing filters for no matches, and an appropriate next action for a new collection. Do not repeat a publishing action already offered by the adjacent `UiActionBanner`.
- The shared component owns spacing, typography, icon presentation and responsive layout. Page classes may identify an empty state for tests or control placement, but must not restyle its surface.

## Catalog action banners

- Use `UiActionBanner` for the closing call to action in task, community, inspiration and marketplace catalogs. Place it inside the results column so it naturally aligns with cards and empty states.
- Supply localized `title` and `summary`, a decorative Lucide `icon` slot (24px, stroke 1.75), and existing `UiButton` actions through the `actions` slot. The component owns the layered icon treatment, surface, typography, spacing and responsive wrapping; pages must not duplicate these styles.
- Actions retain page-level permission checks and existing destinations. Do not advertise publishing or payment flows that the page does not support. Decorative icons do not animate on hover.

## Category navigation

- Category sidebars use `UiCategorySidebar` for their title, icons, counts and selected state. Supply `to` for route links or handle `update:modelValue` for local filters, and use the default slot for contextual footer content. Keep the sidebar mounted while loading; loading and error states belong in the results column.
- The category selection background slides using the shared tab motion tokens. Initial placement and responsive reflow are immediate; reduced-motion preferences disable transitions. Pages should not add their own category selection animations.

## Card decisions and actions

- Use `UiActionCard` inside `UiCatalog` for an icon, title and description that open a destination or start an action. Supply `icon`, `title`, `summary`, and either `to` or a click handler. The component owns icon size, typography, padding and content-driven height; pages control only the catalog columns. Do not place interactive children inside the card.
- Use `UiContentCard layout="media"` with `UiCardMedia`, `UiCardContent` and `UiCardActions` for the shared three-column catalog layout. `UiCatalog grid` stacks the same fields; pages supply content and actions without defining their own media dimensions or grid areas. `UiCardMedia` constrains image, video and document previews independently of their intrinsic dimensions, and accepts `to` when the preview links to details.
- Catalog cards use `UiCardActions` for status, price, license/budget context and the next actions. Use `label`, `value`, `description` (or its slot), and `numeric` for prices/budgets. Keep license and deadline information visible.
- Supply existing `UiButton` / `UiIconButton` components as children. Use compact secondary buttons for opening details and soft buttons for reuse; permissions and destinations stay in the page.
- When the whole card is a link, supply `actionLabel` to render a noninteractive affordance; never nest another link or button inside the card link.
- The component owns spacing, type and responsive layout. Desktop list columns use `--catalog-actions-width`; grid and mobile cards place the shared block at the bottom. Page classes only control placement.
- Align context to the top and actions to the bottom of the block, with a shared left edge. Text buttons share the available width equally and stretch to the same height; icon buttons retain their fixed size.
