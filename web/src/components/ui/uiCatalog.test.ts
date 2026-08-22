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
})
