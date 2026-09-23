import { expect, test } from '@playwright/test'

for (const outcome of ['confirmed', 'still_authenticated', 'unknown_session']) {
  test(`account logout handles ${outcome} without claiming an unconfirmed sign-out`, async ({ page }) => {
    const user = { id: '00000000-0000-4000-8000-000000009851', email: 'buyer@fixture.test', handle: 'buyer', displayName: 'Marketplace Buyer', role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', emailVerified: true, permissions: [] }
    let requestedLogout = false
    let sessionReads = 0
    const unexpected: string[] = []
    await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
    await page.route('**/api/**', route => {
      const path = new URL(route.request().url()).pathname
      if (path === '/api/v1/auth/session') {
        sessionReads++
        if (requestedLogout && outcome === 'unknown_session') return route.fulfill({ status: 503, json: { error: { code: 'unexpected_response', message: 'Session unavailable', retryable: true } } })
        return route.fulfill({ json: { user, authentication: 'session' } })
      }
      if (path === '/api/v1/auth/logout' && route.request().method() === 'POST') {
        requestedLogout = true
        return outcome === 'confirmed' ? route.fulfill({ status: 204 })
          : route.fulfill({ status: 503, json: { error: { code: 'unexpected_response', message: 'Logout unavailable', retryable: true } } })
      }
      if (path === '/api/v1/site-config') return route.fulfill({ json: {
        siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' },
        policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])),
      } })
      if (['/api/v1/auth/oauth/providers', '/api/v1/account/sessions', '/api/v1/account/email-actions', '/api/v1/account/data-rights'].includes(path)) return route.fulfill({ json: { items: [] } })
      if (path === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
      unexpected.push(`${route.request().method()} ${path}`)
      return route.fulfill({ status: 500, json: { error: { code: 'unexpected_response', message: 'Unmocked request', retryable: false } } })
    })
    await page.goto('/settings')
    await expect(page.getByRole('heading', { name: user.displayName, exact: true })).toBeVisible()
    await page.getByRole('button', { name: 'Sign out', exact: true }).click()
    if (outcome === 'confirmed') {
      await expect(page).toHaveURL(/\/auth\?returnTo=/)
      await expect(page.getByLabel('Email', { exact: true })).toBeVisible()
      expect(sessionReads).toBe(1)
    } else {
      if (outcome === 'still_authenticated') {
        await expect(page.locator('.account-page [role="alert"]')).toBeVisible()
        await expect(page).toHaveURL(/\/settings$/)
        await expect(page.getByRole('heading', { name: user.displayName, exact: true })).toBeVisible()
        await expect(page.getByRole('button', { name: 'Sign out', exact: true })).toBeEnabled()
      } else {
        await expect(page).toHaveURL(/\/auth\?returnTo=/)
        await expect(page.locator('.auth-page [role="alert"]')).toBeVisible()
        await expect(page.getByLabel('Email', { exact: true })).toBeVisible()
        await expect(page.getByRole('heading', { name: user.displayName, exact: true })).toHaveCount(0)
      }
      expect(sessionReads).toBe(2)
    }
    expect(unexpected).toEqual([])
  })
}
