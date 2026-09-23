import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

for (const width of [390, 1308]) {
  test(`labeled filters share density, focus and full field widths at ${width}`, async ({ page }) => {
    test.setTimeout(90_000)
    await page.setViewportSize({ width, height: 900 })
    await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('admin') })
    for (const route of ['/workspace/billing', '/workspace/generations', '/admin?tab=users', '/admin?tab=content', '/admin?tab=media', '/admin?tab=governance', '/admin?tab=support', '/admin?tab=generations', '/admin?tab=tasks', '/admin?tab=finance', '/admin?tab=risk']) {
      await page.goto(route)
      const bar = page.locator('.ui-filter-bar--fields').first()
      await expect(bar, route).toBeVisible()
      const trigger = bar.locator('.ui-select__trigger').first()
      const input = bar.locator('.ui-input').first()
      await expect(trigger, route).toHaveCSS('height', '36px')
      await expect(input, route).toHaveCSS('height', '36px')
      const widths = await trigger.evaluate(el => ({ control: el.getBoundingClientRect().width, field: el.closest('label')!.getBoundingClientRect().width }))
      expect(Math.abs(widths.control - widths.field), route).toBeLessThan(1)
      await input.hover()
      await expect(input).toHaveCSS('opacity', '1')
      await expect(input).not.toHaveCSS('cursor', 'not-allowed')
      await input.focus()
      await expect(input).toHaveCSS('box-shadow', 'none')
      await expect.poll(() => input.evaluate(el => el.getAnimations().length)).toBe(0)
      const border = await input.evaluate(el => getComputedStyle(el).borderColor)
      await trigger.click()
      await expect(trigger).toHaveCSS('box-shadow', 'none')
      await expect(trigger).toHaveCSS('border-color', border)
      await page.keyboard.press('Escape')
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), route).toBe(true)
    }
  })

  test(`support fields share form surfaces and remain interactive at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
    await page.goto('/support')
    await page.getByRole('button', { name: 'New case', exact: true }).click()
    const form = page.locator('.support-form.ui-form')
    await expect(form).toBeVisible()
    const input = form.locator('.ui-input').first()
    const trigger = form.locator('.ui-select__trigger').first()
    const textarea = form.locator('.ui-textarea').first()
    await expect(input).toHaveCSS('height', '42px')
    await expect(trigger).toHaveCSS('height', '42px')
    await expect(trigger).toContainText('General support')
    await expect(form).toHaveCSS('margin-top', '24px')
    for (const control of [input, trigger, textarea]) {
      await expect(control).toHaveCSS('background-color', await input.evaluate(el => getComputedStyle(el).backgroundColor))
      await control.hover()
      await expect(control).toHaveCSS('opacity', '1')
      await expect(control).not.toHaveCSS('cursor', 'not-allowed')
    }
    await input.focus()
    await expect.poll(() => input.evaluate(el => el.getAnimations().length)).toBe(0)
    const focusBorder = await input.evaluate(el => getComputedStyle(el).borderColor)
    await input.press('Shift+Tab')
    await expect(trigger).toBeFocused()
    await expect(trigger).toHaveCSS('border-color', focusBorder)
    await input.fill('Shared form field check')
    await expect(input).toHaveValue('Shared form field check')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
}


test('header new-case action initializes a valid category and submits the shared form', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  await page.goto('/support')
  await page.getByRole('button', { name: 'New case', exact: true }).click()
  await expect(page.getByRole('combobox', { name: 'Case category', exact: true })).toContainText('General support')
  const subject = `Header support intake ${Date.now().toString(36)}`
  await page.getByRole('textbox', { name: 'Subject', exact: true }).fill(subject)
  await page.getByRole('textbox', { name: 'Details', exact: true }).fill('Please help me understand where to find the available account settings.')
  await page.getByRole('button', { name: 'Submit case', exact: true }).click()
  await expect(page).toHaveURL(/\/support\/[0-9a-f-]+$/)
  await expect(page.getByRole('heading', { name: subject, exact: true })).toBeVisible()
})
