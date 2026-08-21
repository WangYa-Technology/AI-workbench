import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'

const wcagTags = ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']

const scan = async (page: Page, label: string) => {
  await expect(page.locator('#main-content').first()).toBeVisible()
  await page.waitForTimeout(250)
  const result = await new AxeBuilder({ page }).withTags(wcagTags).analyze()
  const details = result.violations.map((violation) => ({
    rule: violation.id,
    impact: violation.impact,
    help: violation.help,
    nodes: violation.nodes.map((node) => ({
      target: node.target,
      failureSummary: node.failureSummary,
    })),
  }))
  expect(details, `${label} has WCAG violations:\n${JSON.stringify(details, null, 2)}`).toEqual([])
}

test('critical user and operations routes pass automated WCAG 2.2 AA checks', async ({ page }) => {
  test.setTimeout(90_000)
  const visitorRoutes = [
    ['Guest home', '/'],
    ['Public marketplace', '/market'],
    ['Public product', '/market/assets/00000000-0000-4000-8000-000000000503'],
    ['Public tasks', '/market/demands'],
    ['Public task', '/market/demands/00000000-0000-4000-8000-000000000401'],
  ] as const

  await page.request.post('/api/v1/auth/logout')
  await page.goto('/discover')
  for (const theme of ['light', 'dark'] as const) {
    await page.evaluate((value) => globalThis.localStorage.setItem('hcai-theme', value), theme)
    for (const [label, path] of visitorRoutes) {
      await page.goto(path)
      await scan(page, `${label} (${theme})`)
    }
  }

  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()

  const userRoutes = [
    ['Discover', '/discover'],
    ['Create', '/create/image'],
    ['Assets', '/workspace/assets'],
    ['Publish', '/publish'],
    ['Marketplace', '/market'],
    ['Tasks', '/market/demands'],
    ['Community', '/community'],
    ['Search', '/search?q=architectural'],
    ['Account', '/settings'],
    ['Developer Access', '/settings?section=developer'],
  ] as const

  await page.goto('/discover')
  for (const theme of ['light', 'dark'] as const) {
    await page.evaluate((value) => globalThis.localStorage.setItem('hcai-theme', value), theme)
    for (const [label, path] of userRoutes) {
      await page.goto(path)
      await scan(page, `${label} (${theme})`)
    }
  }

  const adminSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(adminSession.ok()).toBeTruthy()
  for (const theme of ['light', 'dark'] as const) {
    await page.evaluate((value) => globalThis.localStorage.setItem('hcai-theme', value), theme)
    await page.goto('/admin')
    await scan(page, `Admin (${theme})`)
    await page.goto('/admin?tab=ranking')
    await scan(page, `Admin ranking operations (${theme})`)
    await page.goto('/admin?tab=tasks')
    await scan(page, `Admin task operations (${theme})`)
    await page.goto('/admin?tab=riskRules')
    await scan(page, `Admin risk rules (${theme})`)
    await page.goto('/admin?tab=models')
    await scan(page, `Admin model routes (${theme})`)
    await page.goto('/admin?tab=settings')
    await scan(page, `Admin system settings (${theme})`)
    await page.goto('/admin?tab=diagnostics')
    await scan(page, `Admin operational diagnostics (${theme})`)
    await page.goto('/admin?tab=developer')
    await scan(page, `Admin Developer Access (${theme})`)
  }
})

test('skip link and route focus remain keyboard operable with reduced motion', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.goto('/discover')

  await page.keyboard.press('Tab')
  const skipLink = page.getByRole('link', { name: 'Skip to main content', exact: true })
  await expect(skipLink).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(page.locator('#main-content')).toBeFocused()

  await page.getByRole('navigation', { name: 'Primary navigation' }).getByRole('link', { name: 'AI creation', exact: true }).click()
  await expect(page).toHaveURL(/\/create\/image$/)
  await expect(page.locator('#main-content')).toBeFocused()
  await expect(page.getByRole('navigation', { name: 'Primary navigation' }).getByRole('link', { name: 'AI creation', exact: true })).toHaveAttribute('aria-current', 'page')
})
