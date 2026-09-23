import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

for (const width of [390, 1308]) {
  test(`shared catalog surfaces and responsive filters at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 700 })
    for (const route of ['/discover', '/market', '/market/demands', '/community']) {
      await page.goto(route)
      const filter = page.locator('.ui-filter-bar')
      await expect(filter).toBeVisible()
      const card = page.locator('.ui-content-card').first()
      await expect(card).toBeVisible()
      await expect(card).toHaveCSS('border-top-width', '1px')
      await expect(card).toHaveCSS('padding-top', width < 768 ? '10px' : '12px')
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      const select = filter.locator('.ui-select__trigger:visible').first()
      await select.click()
      await expect(page.getByRole('listbox')).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(select).toBeFocused()
      const grid = page.locator('.ui-layout-switcher button').last()
      if (await grid.count()) {
        await grid.click()
        await expect(page.locator('.ui-catalog.is-grid')).toBeVisible()
        await expect(card).toHaveCSS('grid-template-columns', /\d/)
      }
    }
  })

  test(`shared detail navigation stays aligned and full width at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 500 })
    await page.goto('/works/00000000-0000-4000-8000-000000000201')
    await expect(page.locator('.detail-toolbar')).toBeVisible()
    await page.evaluate(() => { document.querySelector('#main-content')!.scrollTop = 800; window.scrollTo(0, 800) })
    const floating = page.locator('.content-context-bar-surface')
    await expect(floating).toHaveCSS('opacity', '1')
    const measures = await page.evaluate(() => {
      const root = document.querySelector('#main-content')!
      const bar = document.querySelector('.content-context-bar-surface')!
      const original = document.querySelector('.detail-toolbar a')!
      return { width: bar.getBoundingClientRect().width - root.clientWidth, inset: bar.querySelector('a')!.getBoundingClientRect().x - original.getBoundingClientRect().x }
    })
    expect(Math.abs(measures.width)).toBeLessThan(1)
    expect(Math.abs(measures.inset)).toBeLessThan(1)
    await floating.getByRole('link').click()
    await expect(page).toHaveURL(/\/discover/)
  })
}

test('workspace assets and billing adopt shared components', async ({ page }) => {
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  await page.goto('/workspace/assets')
  await expect(page.locator('.ui-catalog .ui-content-card').first()).toHaveCSS('border-top-width', '1px')
  await page.goto('/workspace/billing')
  await expect(page.locator('[data-page-heading]')).toBeVisible()
  await expect(page.locator('.ui-filter-bar--fields')).toBeAttached()
})

for (const width of [390, 1308]) {
  test(`labeled filters fill their field at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 700 })
    await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
    for (const route of ['/workspace/billing']) {
      await page.goto(route)
      const field = page.locator('.ui-filter-bar--fields .ui-select-root').first()
      await expect(field).toBeAttached()
      const widths = await field.evaluate(el => ({ root: el.getBoundingClientRect().width, trigger: el.querySelector('button')!.getBoundingClientRect().width }))
      expect(Math.abs(widths.root - widths.trigger)).toBeLessThan(1)
    }
  })
}

test('floating context observes a delayed title and updated return destination', async ({ page }) => {
  await page.setViewportSize({ width: 1308, height: 500 })
  await page.goto('/works/00000000-0000-4000-8000-000000000201')
  await expect(page.locator('.detail-toolbar')).toBeVisible()
  await page.evaluate(() => {
    const toolbar = document.querySelector('.detail-toolbar')!
    toolbar.querySelector('a')!.textContent = ''
  })
  await expect(page.locator('.content-context-bar')).toHaveCount(0)
  await page.evaluate(() => {
    const link = document.querySelector('.detail-toolbar a')!
    link.textContent = 'Back to library'
    link.setAttribute('href', '/discover?kind=image')
  })
  await expect(page.locator('.content-context-bar a')).toHaveAttribute('href', '/discover?kind=image')
  await page.evaluate(() => { document.querySelector('#main-content')!.scrollTop = 800; window.scrollTo(0, 800) })
  await expect(page.locator('.content-context-bar')).toHaveClass(/is-visible/)
})

for (const width of [768, 1024]) {
  test(`tablet heroes keep actions separate and stats inside at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    for (const route of ['/discover', '/market/demands', '/support']) {
      await page.goto(route)
      const hero = page.locator('.ui-page-hero')
      await expect(hero).toBeVisible()
      const geometry = await hero.evaluate(el => {
        const bounds = el.getBoundingClientRect()
        const actions = el.querySelector('.page-hero-actions')!.getBoundingClientRect()
        const title = el.querySelector('h1')!.getBoundingClientRect()
        return {
          overlaps: title.left < actions.right && title.right > actions.left && title.top < actions.bottom && title.bottom > actions.top,
          contained: [...el.querySelectorAll('.page-hero-stats article')].every(stat => stat.getBoundingClientRect().bottom <= bounds.bottom && stat.getBoundingClientRect().right <= bounds.right),
        }
      })
      expect(geometry).toEqual({ overlaps: false, contained: true })
    }
  })
}

test('community tools use shared menu keyboard navigation and restore focus', async ({ page }) => {
  await page.goto('/community')
  const trigger = page.locator('.community-tools > button')
  await trigger.focus()
  await page.keyboard.press('ArrowDown')
  const items = page.locator('.community-tools [role="menuitem"]')
  await expect(items.first()).toBeFocused()
  await page.keyboard.press('End')
  await expect(items.last()).toBeFocused()
  await page.keyboard.press('Home')
  await expect(items.first()).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(trigger).toBeFocused()
  await expect(trigger).toHaveAttribute('aria-expanded', 'false')
})
