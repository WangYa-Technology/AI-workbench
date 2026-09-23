import { expect, test } from '@playwright/test'

test('catalog layout controls slide without overshoot and preserve content', async ({ page }) => {
  for (const route of ['/discover', '/community', '/market', '/market/demands']) {
    await page.goto(route)
    const control = page.locator('.ui-layout-switcher')
    await expect(control).toBeVisible()
    const catalog = page.locator('.ui-catalog').first()
    const count = await catalog.locator('.ui-content-card').count()
    expect(count).toBeGreaterThan(0)
    const samples = await control.evaluate(async element => {
      const indicator = element.querySelector<HTMLElement>('.ui-layout-switcher__indicator')!
      const buttons = element.querySelectorAll('button')
      const start = indicator.getBoundingClientRect().x
      const end = buttons[1]!.getBoundingClientRect().x
      buttons[1]!.click()
      const positions: number[] = []
      const began = performance.now()
      while (performance.now() - began < 350) {
        await new Promise(requestAnimationFrame)
        positions.push(indicator.getBoundingClientRect().x)
      }
      return { start, end, positions }
    })
    expect(samples.positions.some(x => x > samples.start && x < samples.end)).toBeTruthy()
    for (const [index, x] of samples.positions.entries()) {
      expect(x).toBeGreaterThanOrEqual((samples.positions[index - 1] ?? samples.start) - 0.1)
      expect(x).toBeLessThanOrEqual(samples.end + 0.1)
    }
    await expect(control.getByRole('button').last()).toHaveAttribute('aria-pressed', 'true')
    await expect(catalog).toHaveClass(/is-grid/)
    await expect(catalog.locator('.ui-content-card')).toHaveCount(count)
    await control.getByRole('button').first().click()
    await expect(catalog).not.toHaveClass(/is-grid/)
  }
})

test('community grid supports keyboard switching and reduced motion on mobile', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.goto('/community')
  const control = page.locator('.ui-layout-switcher')
  const grid = control.getByRole('button').last()
  await grid.focus()
  await page.keyboard.press('Enter')
  await expect(grid).toHaveAttribute('aria-pressed', 'true')
  expect(await control.locator('.ui-layout-switcher__indicator').evaluate(element => parseFloat(getComputedStyle(element).transitionDuration))).toBeLessThanOrEqual(0.00001)
  await expect(page.locator('.community-feed')).toHaveClass(/is-grid/)
  const card = page.locator('.community-post-row').first()
  await expect(card.locator('.community-post-thumbnail')).toBeVisible()
  await expect(card.locator('.ui-card-content__meta a')).toBeVisible()
  await expect(card.locator('.community-post-stat')).toHaveCount(2)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(390)
  await card.locator('.ui-card-content__title a').click()
  await expect(page).toHaveURL(/\/community\/posts\//)
})
