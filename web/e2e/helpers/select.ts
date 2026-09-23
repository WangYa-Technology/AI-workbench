import { expect, type Locator } from '@playwright/test'

/** Exercise the visible UiSelect instead of trying to operate its hidden form mirror. */
export async function chooseOption(control: Locator, value: string) {
  await expect(control).toBeAttached()
  const root = control.locator('xpath=ancestor-or-self::*[contains(concat(" ", normalize-space(@class), " "), " ui-select-root ")][1]')
  if (await root.count() === 0) {
    await control.selectOption(value)
    return
  }
  const option = root.locator('select option')
  const label = await option.evaluateAll((options, wanted) => options.find(el => (el as HTMLOptionElement).value === wanted)?.textContent?.trim(), value)
  expect(label, `option ${value} exists`).toBeTruthy()
  const trigger = root.getByRole('combobox')
  await trigger.click()
  const id = await trigger.getAttribute('aria-controls')
  await control.page().locator(`[id="${id}"]`).getByRole('option', { name: label!, exact: true }).click()
  await expectSelection(trigger, value)
}

export async function expectSelection(control: Locator, value: string) {
  await expect(control).toBeAttached()
  const root = control.locator('xpath=ancestor-or-self::*[contains(concat(" ", normalize-space(@class), " "), " ui-select-root ")][1]')
  if (await root.count() === 0) { await expect(control).toHaveValue(value); return }
  await expect(root.locator('select')).toHaveValue(value)
  const label = await root.locator('select option:checked').textContent()
  await expect(root.getByRole('combobox')).toHaveText(label?.trim() || '')
}
