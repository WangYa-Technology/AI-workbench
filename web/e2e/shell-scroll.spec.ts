import { expect, test } from '@playwright/test'

test('keeps the admin heading fixed while the admin body owns scrolling', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(session.ok()).toBeTruthy()

  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto('/admin?tab=generations')
  await expect(page.locator('.admin-user-directory')).toBeVisible()

  const content = page.locator('.app-main > #main-content')
  const adminBody = page.locator('.admin-page-scroll')
  const adminDirectory = page.locator('.admin-user-directory')
  const adminHeader = page.locator('.admin-topbar-title')
  const adminSectionHeading = page.locator('.admin-section-heading')
  const adminFilters = page.locator('.admin-page-scroll > .admin-user-directory > .admin-user-filters')
  await expect(page.locator('.global-search')).toHaveCount(0)
  await expect(adminHeader.locator('h1')).toBeVisible()
  await expect(adminHeader.locator(':scope > *')).toHaveCount(1)
  await expect(adminSectionHeading.locator('h2')).toBeVisible()
  await expect(content.locator('.admin-header, .admin-topbar-title')).toHaveCount(0)
  await adminDirectory.evaluate((element) => {
    const scrollFixture = document.createElement('div')
    scrollFixture.dataset.testScrollFixture = 'true'
    scrollFixture.style.height = '1000px'
    element.append(scrollFixture)
  })
  const initial = await adminBody.evaluate(element => ({
    clientHeight: element.clientHeight,
    scrollHeight: element.scrollHeight,
  }))
  expect(initial.scrollHeight).toBeGreaterThan(initial.clientHeight)
  const headerTop = await adminHeader.evaluate(element => element.getBoundingClientRect().top)
  const sectionHeadingTop = await adminSectionHeading.evaluate(element => element.getBoundingClientRect().top)
  const filtersTop = await adminFilters.evaluate(element => element.getBoundingClientRect().top)

  await adminBody.evaluate(element => { element.scrollTop = 360 })
  const scrolled = await adminBody.evaluate(element => ({
    scrollTop: element.scrollTop,
  }))
  expect(scrolled.scrollTop).toBeGreaterThan(0)
  await expect.poll(() => content.evaluate(element => element.scrollTop)).toBe(0)
  await expect.poll(() => adminHeader.evaluate(element => element.getBoundingClientRect().top)).toBeCloseTo(headerTop, 1)
  await expect.poll(() => adminSectionHeading.evaluate(element => element.getBoundingClientRect().top)).toBeCloseTo(sectionHeadingTop, 1)
  await expect.poll(() => adminFilters.evaluate(element => element.getBoundingClientRect().top)).toBeCloseTo(filtersTop, 1)
  await expect.poll(() => page.evaluate(() => globalThis.scrollY)).toBe(0)

  await page.goto('/admin?tab=settings')
  await expect(page.locator('.system-settings-admin')).toBeVisible()
  await expect.poll(() => adminBody.evaluate(element => element.scrollTop)).toBe(0)

  await page.getByRole('navigation', { name: 'Primary navigation' }).getByRole('link', { name: 'Back to workspace', exact: true }).click()
  await page.getByRole('navigation', { name: 'Primary navigation' }).getByRole('link', { name: 'Community', exact: true }).click()
  await expect(page).toHaveURL('/community')
  await expect.poll(() => content.evaluate(element => element.scrollTop)).toBe(0)

  await page.setViewportSize({ width: 820, height: 900 })
  await page.goto('/discover')
  const tabletLayout = await page.evaluate(() => {
    const frame = document.querySelector<HTMLElement>('.app-main > #main-content')!
    const navigation = document.querySelector<HTMLElement>('.mobile-nav')!
    const footer = document.querySelector<HTMLElement>('.site-footer')!
    return {
      frameBottom: frame.getBoundingClientRect().bottom,
      footerTop: footer.getBoundingClientRect().top,
      footerBottom: footer.getBoundingClientRect().bottom,
      navigationTop: navigation.getBoundingClientRect().top,
    }
  })
  expect(tabletLayout.footerTop).toBeCloseTo(tabletLayout.frameBottom, 3)
  expect(tabletLayout.footerBottom).toBeCloseTo(tabletLayout.navigationTop, 3)

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/discover')
  const mobile = await content.evaluate(element => ({
    overflowY: getComputedStyle(element).overflowY,
    documentHeight: document.documentElement.scrollHeight,
    viewportHeight: globalThis.innerHeight,
  }))
  expect(mobile.overflowY).toBe('visible')
  expect(mobile.documentHeight).toBeGreaterThan(mobile.viewportHeight)
})

test('collapses the desktop sidebar without changing mobile navigation', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()

  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto('/discover')
  await page.evaluate(() => localStorage.removeItem('hcai-sidebar-collapsed'))
  await page.reload()

  const shell = page.locator('.app-shell')
  const sidebar = page.locator('.site-sidebar')
  const collapse = page.getByRole('button', { name: 'Collapse sidebar', exact: true })
  await expect(collapse).toBeVisible()
  await expect.poll(() => sidebar.evaluate(element => element.getBoundingClientRect().width)).toBe(202)

  await collapse.click()
  await expect(shell).toHaveClass(/is-sidebar-collapsed/)
  await expect.poll(() => sidebar.evaluate(element => element.getBoundingClientRect().width)).toBe(64)
  await expect(page.locator('.primary-nav a span').first()).toBeHidden()
  await expect(page.getByRole('navigation', { name: 'Primary navigation' }).getByRole('link', { name: 'Community', exact: true })).toBeVisible()

  await page.reload()
  await expect(shell).toHaveClass(/is-sidebar-collapsed/)
  await expect(page.getByRole('button', { name: 'Expand sidebar', exact: true })).toBeVisible()

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(sidebar).toBeHidden()
  await expect(page.locator('.sidebar-collapse-control')).toBeHidden()
  await expect(page.getByRole('navigation', { name: 'Mobile navigation' }).getByRole('link')).toHaveCount(5)
  await expect(page.locator('.mobile-nav')).toBeVisible()
})

test('toggles the theme from the header icon button', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()

  await page.addInitScript(() => localStorage.setItem('hcai-theme', 'light'))
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto('/discover')

  const themeSwitch = page.getByRole('button', { name: 'Switch color theme', exact: true })
  await expect(themeSwitch).toBeVisible()
  await themeSwitch.click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await expect(page.locator('html')).toHaveAttribute('data-theme-transition', 'running')
  await expect(page.locator('html')).not.toHaveAttribute('data-theme-transition')

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(themeSwitch).toBeHidden()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(0)
})
