import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'

const user = { id: '00000000-0000-4000-8000-000000000001', email: 'member@fixture.hcai.test', displayName: 'Fixture member', handle: 'fixture', role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', emailVerified: true, permissions: [] }
const task = { id: 'open-one', status: 'open', title: 'Product launch film', summary: 'A short film for a new collection', budgetCents: 12000, currency: 'USD', deadline: '2027-01-12T10:00:00Z', client: { displayName: 'Fixture studio' } }
const product = { id: 'resource-one', title: 'Editorial texture pack', mediaUrl: '', mediaKind: 'image', priceCents: 2400, currency: 'USD', seller: { displayName: 'Fixture seller' }, license: { name: 'Personal license' } }
async function mockHome(page: Page, options: { locale?: string; theme?: string; works?: unknown[]; tasks?: unknown[]; products?: unknown[]; fail?: string; authenticated?: boolean } = {}) {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.addInitScript(({ locale, theme }) => {
    localStorage.setItem('hcai-locale', locale)
    localStorage.setItem('hcai-theme', theme)
  }, { locale: options.locale || 'en-US', theme: options.theme || 'dark' })
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname
    if (path === '/api/v1/auth/session') return options.authenticated ? route.fulfill({ json: { user } }) : route.fulfill({ status: 401, json: { error: { code: 'authentication_required', message: 'Sign in', retryable: false } } })
    if (path === '/api/v1/site-config') return route.fulfill({ json: { siteName: 'HCAI CHAT', siteIconUrl: '/brand/logo.png', serverUrl: 'http://localhost', footerText: { enUS: 'Local test', zhCN: '本地测试' }, policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])) } })
    if (path === `/api/v1/${options.fail}`) return route.fulfill({ status: 503, json: { error: { code: 'unavailable', message: 'Unavailable', retryable: true } } })
    if (path === '/api/v1/works') return route.fulfill({ json: { items: options.works || [] } })
    if (path === '/api/v1/tasks') return route.fulfill({ json: { items: options.tasks || [], total: options.tasks?.length || 0, typeCounts: {}, nextCursor: null } })
    if (path === '/api/v1/products') return route.fulfill({ json: { items: options.products || [] } })
    if (path === '/api/v1/meta') return route.fulfill({ json: { taskPaymentProvider: { enabled: false, liveMode: false }, paymentProvider: { enabled: false, liveMode: false } } })
    if (path === '/api/v1/task-types') return route.fulfill({ json: { items: [] } })
    return route.fulfill({ status: 404, json: { error: { code: 'not_found', message: 'Not found', retryable: false } } })
  })
  return errors
}

for (const locale of ['en-US', 'zh-CN']) for (const theme of ['light', 'dark']) {
  test(`${locale} ${theme}: readable business home across widths with animation enabled`, async ({ page }) => {
    const errors = await mockHome(page, { locale, theme, tasks: [task], products: [product] })
    await page.goto('/')
    await expect(page.locator('.home-task')).toHaveCount(1)
    for (const width of [320, 390, 768, 1024, 1744]) {
      await page.setViewportSize({ width, height: 900 })
      const layout = await page.evaluate(() => {
        const cta = document.querySelector('.home-publish')!.getBoundingClientRect()
        return { width: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth, ctaLeft: cta.left, ctaRight: cta.right, ctaBottom: cta.bottom }
      })
      expect(layout.scroll, `overflow at ${width}`).toBe(layout.width)
      expect(layout.ctaLeft).toBeGreaterThan(0)
      expect(layout.ctaRight).toBeLessThan(width)
      if (width <= 390) expect(layout.ctaBottom).toBeLessThan(844)
    }
    // Check actual motion, rather than disabling it to hide contrast regressions.
    const motion = await page.locator('.home-hero-copy').evaluate(el => ({ name: getComputedStyle(el).animationName, opacity: getComputedStyle(el).opacity }))
    expect(motion.name).toContain('home-rise')
    expect(motion.opacity).toBe('1')
    const scan = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze()
    expect(scan.violations.map(v => ({ id: v.id, targets: v.nodes.map(n => n.target) }))).toEqual([])
    expect(errors).toEqual([])
  })
}

test('primary publishing entry preserves intent through guest login', async ({ page }) => {
  const errors = await mockHome(page)
  await page.goto('/')
  await page.locator('.home-publish').click()
  await expect(page).toHaveURL(/\/auth\?/)
  expect(new URL(page.url()).searchParams.get('returnTo')).toBe('/market/demands?publish=1')
  expect(errors).toEqual([])
})

test('publishing return opens the actual form and closing it consumes the intent', async ({ page }) => {
  const errors = await mockHome(page, { authenticated: true })
  await page.goto('/market/demands?publish=1')
  await expect(page.getByRole('dialog')).toBeVisible()
  await expect(page.getByRole('dialog').locator('input').first()).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page).toHaveURL(/\/market\/demands$/)
  await page.reload()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  expect(errors).toEqual([])
})

test('project and resource entry points work even without public content', async ({ page }) => {
  await mockHome(page)
  await page.goto('/')
  await expect(page.locator('.home-catalog-notice')).toHaveCount(0)
  await expect(page.locator('.home-task, .home-product, .home-work')).toHaveCount(0)
  await page.locator('.home-pathway-projects').click()
  await expect(page).toHaveURL(/\/market\/demands$/)
  await page.goto('/')
  await page.locator('.home-pathway-resources').click()
  await expect(page).toHaveURL(/\/market$/)
})

test('task failure preserves published work and products and can be retried', async ({ page }) => {
  await mockHome(page, { fail: 'tasks', products: [product], works: [{ id: 'work-one', title: 'Published study', mediaKind: 'image', mediaUrl: '/media/home-cinematic.jpg', author: { displayName: 'Fixture creator' } }] })
  await page.goto('/')
  await expect(page.locator('.home-work')).toHaveAttribute('href', '/works/work-one')
  await expect(page.locator('.home-product')).toHaveAttribute('href', '/market/assets/resource-one')
  await expect(page.locator('.home-product')).toContainText('Personal license')
  await expect(page.locator('.home-catalog-notice')).toContainText('Some content could not load')
  await page.route('**/api/v1/tasks?**', route => route.fulfill({ json: { items: [task] } }))
  await page.getByRole('button', { name: 'Try again', exact: true }).click()
  await expect(page.locator('.home-task')).toHaveCount(1)
  await expect(page.locator('.home-catalog-notice')).toHaveCount(0)
})

test('product failure preserves open tasks and filters out closed tasks', async ({ page }) => {
  await mockHome(page, { fail: 'products', tasks: [task, { ...task, id: 'closed-one', status: 'accepted' }] })
  await page.goto('/')
  await expect(page.locator('.home-task')).toHaveCount(1)
  await expect(page.locator('.home-task')).toHaveAttribute('href', '/market/demands/open-one')
  await expect(page.locator('.home-task')).toContainText('$120.00')
})

test('reduced motion keeps all content readable and FAQ keyboard accessible', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await mockHome(page)
  await page.goto('/')
  expect(await page.locator('.home-hero-copy').evaluate(el => getComputedStyle(el).animationName)).toBe('none')
  const summary = page.locator('.home-faq button').nth(1)
  await summary.focus()
  await page.keyboard.press('Enter')
  await expect(page.locator('.home-faq button').nth(1)).toHaveAttribute('aria-expanded', 'true')
  await expect(page.locator('.home-faq .ui-collapsible').nth(1)).toContainText('You can use eligible assets you own')
})

test('existing signed-in home redirect is preserved', async ({ page }) => {
  await mockHome(page, { authenticated: true })
  await page.goto('/')
  await expect(page).toHaveURL(/\/discover$/)
})
