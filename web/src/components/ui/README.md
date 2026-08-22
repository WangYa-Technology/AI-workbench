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
