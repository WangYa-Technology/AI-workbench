import { expect, test, type Page } from '@playwright/test'

const policies = [
  ['terms', 'Terms of Service'],
  ['privacy', 'Privacy'],
  ['cookies', 'Cookies'],
  ['acceptable', 'Acceptable Use'],
  ['ai', 'AI disclosure'],
  ['licensing', 'Licensing'],
  ['refunds', 'Refunds'],
  ['copyright', 'Copyright'],
] as const

function collectRuntimeIssues(page: Page) {
  const issues: string[] = []
  let anonymousSessionResponses = 0
  page.on('response', (response) => {
    if (response.status() < 400) return
    const path = new URL(response.url()).pathname
    if (response.status() === 401 && path === '/api/v1/auth/session') {
      anonymousSessionResponses += 1
      return
    }
    issues.push(`HTTP ${response.status()} ${path}`)
  })
  page.on('console', (message) => {
    if (!['warning', 'error'].includes(message.type())) return
    if (message.text() === 'Failed to load resource: the server responded with a status of 401 (Unauthorized)') return
    issues.push(message.text())
  })
  page.on('pageerror', (error) => issues.push(error.message))
  return { issues, anonymousSessionResponses: () => anonymousSessionResponses }
}

test('exposes every policy and private support handoff from the public shell', async ({ page }) => {
  const runtime = collectRuntimeIssues(page)

  await page.request.post('/api/v1/auth/logout')
  await page.goto('/discover')
  const footer = page.locator('.site-footer')
  await expect(footer).toBeVisible()
  await expect(footer).toContainText('Production legal acceptance pending')
  await expect(footer.getByRole('link')).toHaveCount(9)
  for (const [topic, title] of policies) {
    const link = footer.getByRole('link', { name: title, exact: true })
    await expect(link).toHaveAttribute('href', `/policies/${topic}`)
    await link.click()
    await expect(page).toHaveURL(new RegExp(`/policies/${topic}$`))
    await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
  }

  await page.getByRole('link', { name: 'Open support workspace', exact: true }).click()
  await expect(page).toHaveURL(/\/support$/)
  await expect(page.getByRole('heading', { name: 'Sign in to contact support', exact: true })).toBeVisible()
  expect(runtime.anonymousSessionResponses()).toBe(1)
  expect(runtime.issues).toEqual([])
})

test('keeps localized policy access within a dark mobile viewport', async ({ page }) => {
  const runtime = collectRuntimeIssues(page)

  await page.setViewportSize({ width: 390, height: 844 })
  await page.addInitScript(() => {
    globalThis.localStorage.setItem('hcai-locale', 'zh-CN')
    globalThis.localStorage.setItem('hcai-theme', 'dark')
  })
  await page.goto('/policies/privacy')

  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await expect(page.getByRole('heading', { name: '隐私', exact: true })).toBeVisible()
  const footer = page.locator('.site-footer')
  await expect(footer).toContainText('上线前仍需法律验收')
  await expect(footer.getByRole('link')).toHaveCount(9)
  await page.evaluate(() => globalThis.scrollTo(0, document.documentElement.scrollHeight))
  const layout = await page.evaluate(() => {
    const footer = document.querySelector('.site-footer')?.getBoundingClientRect()
    const mobileNav = document.querySelector('.mobile-nav')?.getBoundingClientRect()
    return {
      client: document.documentElement.clientWidth,
      scroll: document.documentElement.scrollWidth,
      footerBottom: footer?.bottom ?? Number.POSITIVE_INFINITY,
      mobileNavTop: mobileNav?.top ?? Number.NEGATIVE_INFINITY,
    }
  })
  expect(layout.scroll).toBe(layout.client)
  expect(layout.footerBottom).toBeLessThanOrEqual(layout.mobileNavTop)
  expect(runtime.anonymousSessionResponses()).toBe(1)
  expect(runtime.issues).toEqual([])
})
