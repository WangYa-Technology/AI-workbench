import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

test('community distinguishes an empty category, an empty collection, and my posts', async ({ page }) => {
  await page.goto('/community?category=community_tutorial')
  const empty = page.locator('.community-empty')
  await expect(empty).toHaveClass(/ui-empty-state/)
  await expect(empty.getByRole('heading')).toHaveText('No discussions match')
  await expect(empty).not.toContainText('There are no Community posts yet.')
  await empty.getByRole('button', { name: 'Clear filters' }).click()
  await expect(page.locator('.community-post-row').first()).toBeVisible()
  await expect(empty).toHaveCount(0)

  await page.route('**/api/v1/community/posts*', route => route.fulfill({ json: { items: [], nextCursor: null } }))
  await page.reload()
  await expect(empty.getByRole('heading')).toHaveText('There are no Community posts yet.')
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('publisher') })).ok()).toBeTruthy()
  await page.goto('/community?view=mine')
  await expect(empty.getByRole('heading')).toHaveText('You have not published a post yet')
  await expect(empty.getByRole('button', { name: 'Publish post' })).toHaveCount(0)
  await expect(page.locator('.ui-action-banner').getByRole('button', { name: 'Publish post' })).toBeVisible()
  await empty.getByRole('button', { name: 'All posts' }).click()
  await expect(page).not.toHaveURL(/view=mine/)
})

test('shared empty catalogs fit narrow and wide viewports in both themes', async ({ page }, testInfo) => {
  for (const path of ['/community?category=community_tutorial', '/discover?q=unmatched-empty-state', '/market?q=unmatched-empty-state', '/market/demands?q=unmatched-empty-state', '/search?q=unmatched-empty-state']) {
    await page.goto(path)
    const empty = page.locator('.ui-empty-state').first()
    await expect(empty).toBeVisible()
    for (const theme of ['light', 'dark']) {
      await page.evaluate(theme => document.documentElement.dataset.theme = theme, theme)
      for (const width of [1551, 768, 390, 320]) {
        await page.setViewportSize({ width, height: 901 })
        await empty.scrollIntoViewIfNeeded()
        const fits = await empty.evaluate(element => {
          const box = element.getBoundingClientRect()
          return box.left >= 0 && box.right <= innerWidth && [...element.querySelectorAll('h2, p, button, a')].every(child => {
            const rect = child.getBoundingClientRect()
            return rect.left >= box.left && rect.right <= box.right && child.scrollWidth <= child.clientWidth + 1
          })
        })
        expect(fits).toBeTruthy()
      }
    }
    if (path.startsWith('/community')) await page.screenshot({ path: testInfo.outputPath('community-empty-mobile-dark.png') })
  }
})

test('community loading and failure do not masquerade as empty content', async ({ page }) => {
  let release: () => void = () => {}
  const pending = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/v1/community/posts*', async route => { await pending; await route.abort() })
  await page.goto('/community?category=community_tutorial')
  await expect(page.locator('.category-content .page-state')).toBeVisible()
  await expect(page.locator('.community-empty')).toHaveCount(0)
  release()
  await expect(page.locator('.category-content [role="alert"]')).toBeVisible()
  await expect(page.locator('.community-empty')).toHaveCount(0)
})

test('task and product empty filters clear back to populated lists', async ({ page }) => {
  for (const [path, results] of [['/market/demands', '.task-row'], ['/market', '.product-card']]) {
    await page.goto(`${path}?q=unmatched-empty-state`)
    const empty = page.locator('.ui-empty-state')
    await expect(empty).toBeVisible()
    await empty.getByRole('button', { name: 'Clear filters' }).click()
    await expect(page.locator(results).first()).toBeVisible()
    await expect(empty).toHaveCount(0)
  }
})
