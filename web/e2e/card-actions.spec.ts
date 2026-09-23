import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

test('asset media proportions cannot stretch the shared list or grid layout', async ({ page }) => {
  await page.setViewportSize({ width: 1528, height: 901 })
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  await page.route('**/api/v1/assets', async route => {
    const response = await route.fetch()
    const body = await response.json()
    const asset = body.items[0]
    body.items = [[1200, 600], [600, 1200], [800, 800]].map(([width, height], index) => ({ ...asset, id: `${asset.id}-${index}`, width, height }))
    await route.fulfill({ response, json: body })
  })
  await page.goto('/workspace/assets')
  const cards = page.locator('.asset-row')
  await expect(cards).toHaveCount(3)
  for (const grid of [false, true]) {
    await page.getByRole('button', { name: grid ? 'Grid view' : 'List view', exact: true }).click()
    const sizes = await cards.evaluateAll(elements => elements.map(element => {
      const box = element.getBoundingClientRect()
      const media = element.querySelector('.ui-card-media')!.getBoundingClientRect()
      const actions = element.querySelector('.ui-card-actions')!.getBoundingClientRect()
      return { height: box.height, mediaHeight: media.height, actionsInside: actions.bottom <= box.bottom && actions.right <= box.right }
    }))
    expect(new Set(sizes.map(size => size.height)).size).toBe(1)
    expect(sizes.every(size => size.mediaHeight === (grid ? 172 : 128) && size.actionsInside)).toBe(true)
  }
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.goto('/market/demands')
  await expect(page.locator('.task-row .ui-card-media').first()).toHaveCSS('height', '168px')
  await page.setViewportSize({ width: 1528, height: 901 })
  await expect(page.locator('.task-row .ui-card-media').first()).toHaveCSS('height', '128px')
})

for (const locale of ['en-US', 'zh-CN']) {
  test(`catalog action areas preserve readable context and controls in ${locale}`, async ({ page }) => {
    await page.addInitScript(locale => localStorage.setItem('hcai-locale', locale), locale)
    expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
    for (const path of ['/discover', '/market', '/market/demands', '/workspace/assets']) {
      await page.goto(path)
      const card = page.locator('.ui-content-card').first()
      const actions = card.locator('.ui-card-actions')
      await expect(actions).toBeVisible()
      await expect(actions.locator('.ui-card-actions__value')).not.toBeEmpty()
      await expect(actions.locator('.ui-card-actions__description')).not.toBeEmpty()
      for (const grid of [false, true]) {
        const switcher = page.locator('.ui-layout-switcher')
        if (grid && !await switcher.count()) continue
        if (await switcher.count()) await switcher.getByRole('button').nth(grid ? 1 : 0).click()
        for (const theme of ['light', 'dark']) {
          await page.evaluate(theme => { document.documentElement.dataset.theme = theme }, theme)
          for (const width of [1551, 768, 390, 320]) {
            await page.setViewportSize({ width, height: 901 })
            const fits = await actions.evaluate(element => {
              const card = element.closest('.ui-content-card')!.getBoundingClientRect()
              const box = element.getBoundingClientRect()
              const controls = [...element.querySelectorAll<HTMLElement>('.ui-button, .ui-icon-button')]
              return box.left >= card.left && box.right <= card.right + 1 && box.right <= innerWidth
                && controls.length > 0
                && controls.every(control => {
                  const rect = control.getBoundingClientRect()
                  return rect.width > 0 && rect.left >= box.left && rect.right <= box.right + 1
                    && control.scrollWidth <= control.clientWidth + 1
                })
                && element.scrollWidth <= element.clientWidth + 1
            })
            expect(fits, `${path} ${width}px ${theme} grid=${grid}`).toBeTruthy()
          }
        }
      }
      if (path === '/market' || path === '/market/demands') {
        await expect(card.locator('a, button, [tabindex]')).toHaveCount(0)
        await card.focus()
        await page.keyboard.press('Enter')
        await expect(page).toHaveURL(path === '/market' ? /\/market\/assets\// : /\/market\/demands\/[\da-f-]+/)
      }
    }
  })
}
