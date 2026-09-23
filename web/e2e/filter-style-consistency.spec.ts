import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

for (const width of [390, 1308]) {
  test(`catalog filters share surfaces and search focus at ${width}`, async ({ page }) => {
    await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
    await page.setViewportSize({ width, height: 901 })
    let reference: string[] | undefined
    for (const route of ['/community', '/market/demands', '/notifications', '/discover', '/market']) {
      await page.goto(route)
      const bar = page.locator('.ui-filter-bar').first()
      await expect(bar).toBeVisible()
      const triggers = bar.locator('.ui-select-root .ui-select__trigger:visible')
      await expect(triggers.first()).toBeVisible()
      const styles = await triggers.evaluateAll(elements => elements.map(element => {
        const style = getComputedStyle(element)
        return ['height', 'font-size', 'font-weight', 'border-radius', 'border-color', 'background-color', 'padding', 'gap'].map(property => style.getPropertyValue(property))
      }))
      reference ??= styles[0]
      for (const style of styles) expect(style, route).toEqual(reference)
      await triggers.first().click()
      await expect(page.getByRole('listbox')).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(triggers.first()).toBeFocused()
      const search = bar.locator('.ui-filter-bar__search')
      if (await search.count()) {
        await expect(search).toHaveCSS('height', '40px')
        await search.locator('input').fill('test')
        await expect(search.locator('input')).toBeFocused()
        await expect(search.locator('input')).toHaveCSS('box-shadow', 'none')
        const inputBox = await search.locator('input').boundingBox()
        const wrapperBox = await search.boundingBox()
        expect(inputBox!.x + inputBox!.width).toBeLessThanOrEqual(wrapperBox!.x + wrapperBox!.width)
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), route).toBe(true)
    }
  })
}
