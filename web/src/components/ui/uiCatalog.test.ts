import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import * as components from './index'
import { addDays, dateKey, parseDate, startOfCalendarMonth } from './uiDate'
import { hexToHsl, hslToHex, normalizeHex } from './uiColor'

const appicaCatalog = [
  'UiAccordion', 'UiAlert', 'UiAlertDialog', 'UiAutocomplete', 'UiAvatar', 'UiBackgroundPattern',
  'UiBadge', 'UiBorderBeam', 'UiBreadcrumb', 'UiButton', 'UiButtonGroup', 'UiCalendar', 'UiCard',
  'UiCarousel', 'UiCheckbox', 'UiChip', 'UiCollapsible', 'UiColorArea', 'UiColorPicker', 'UiColorSlider',
  'UiColorSwatch', 'UiColorSwatchPicker', 'UiCombobox', 'UiContextMenu', 'UiCopyButton', 'UiCountdown',
  'UiDataTable', 'UiDateField', 'UiDatePicker', 'UiDialog', 'UiDrawer', 'UiDropdownMenu', 'UiField',
  'UiForm', 'UiGradientGlow', 'UiInput', 'UiKbd', 'UiLoader', 'UiMenubar', 'UiMeter', 'UiNavigation',
  'UiNavigationMenu', 'UiNumberField', 'UiOtpField', 'UiPagination', 'UiPopover', 'UiPreviewCard',
  'UiProgress', 'UiRadio', 'UiRating', 'UiScrollArea', 'UiSelect', 'UiSeparator', 'UiSkeleton', 'UiSlider',
  'UiSparkline', 'UiSpinner', 'UiSwitch', 'UiTable', 'UiTabs', 'UiTextAnimate', 'UiTextarea', 'UiThumbnail',
  'UiTimeField', 'UiToast', 'UiToc', 'UiToggle', 'UiToggleGroup', 'UiToolbar', 'UiTooltip',
] as const

describe('Appica Vue compatibility catalog', () => {
  it('exports an implementation for all 70 official catalog entries', () => {
    expect(appicaCatalog).toHaveLength(70)
    for (const name of appicaCatalog) expect(components[name]).toBeTruthy()
  })

  it('keeps local date values timezone-safe', () => {
    const date = parseDate('2026-08-22')
    expect(date).toBeDefined()
    expect(dateKey(date!)).toBe('2026-08-22')
    expect(dateKey(addDays(date!, 1))).toBe('2026-08-23')
    expect(dateKey(startOfCalendarMonth(new Date(2026, 7, 1), 1))).toBe('2026-07-27')
    expect(parseDate('2026-02-30')).toBeUndefined()
  })

  it('normalizes and round-trips hex colors', () => {
    expect(normalizeHex('#1f63e9')).toBe('#1F63E9')
    expect(normalizeHex('abc')).toBe('#AABBCC')
    expect(normalizeHex('invalid')).toBeUndefined()
    expect(hslToHex(hexToHsl('#1F63E9'))).toBe('#2063E9')
  })

  it('keeps focus rings on the primary accent instead of semantic green', () => {
    const tokens = readFileSync(new URL('../../styles/tokens.css', import.meta.url), 'utf8')
    const focusValues = [...tokens.matchAll(/--focus:\s*([^;]+);/g)].map(match => match[1].trim())
    expect(focusValues).toEqual(['var(--accent)', 'var(--accent-readable)'])
    expect(tokens.match(/--focus-ring:\s*0 0 0 2px/g)).toHaveLength(2)
  })

  it('centralizes table header presentation tokens', () => {
    const tokens = readFileSync(new URL('../../styles/tokens.css', import.meta.url), 'utf8')
    const ui = readFileSync(new URL('../../styles/ui.css', import.meta.url), 'utf8')
    expect(tokens).toMatch(/--data-header-height:\s*42px/)
    expect(tokens).toMatch(/--data-header-font-size:\s*12px/)
    expect(ui).toMatch(/\.ui-table th \{[^}]*var\(--data-header-height\)[^}]*var\(--data-header-font-size\)/s)
  })
})
