import { expect, test } from '@playwright/test'

// Browser integration with the real store/router/components and mocked APIs.
// No mail, running database, credentials or payment provider is used.
for (const method of ['password', 'code', 'registration']) {
  test(`${method} authentication returns to marketplace orders with the accepted session`, async ({ page }) => {
    const user = { id: '00000000-0000-4000-8000-000000009801', email: 'buyer@fixture.test', handle: 'buyer', displayName: 'Buyer', role: 'member', status: 'active', locale: 'en-US', timezone: 'UTC', emailVerified: true, permissions: [] }
    const challenge = { challengeId: 'challenge-fixture', expiresAt: '2099-01-01T00:00:00Z', retryAfterSeconds: 0 }
    const unexpected: string[] = []
    let authenticated = false
    let authWrites = 0
    let orderReads = 0
    await page.addInitScript(() => localStorage.setItem('hcai-locale', 'en-US'))
    await page.route('**/api/**', route => {
      const path = new URL(route.request().url()).pathname
      if (path === '/api/v1/auth/session') return authenticated
        ? route.fulfill({ json: { user, authentication: 'session' } })
        : route.fulfill({ status: 401, json: { error: { code: 'authentication_required', message: 'Sign in', retryable: false } } })
      if (path === '/api/v1/site-config') return route.fulfill({ json: {
        siteName: 'HCAI CHAT', serverUrl: 'http://localhost', siteIconUrl: '/brand/logo.png', footerText: { enUS: '', zhCN: '' },
        policies: Object.fromEntries(['terms', 'privacy', 'cookies', 'acceptable', 'ai', 'licensing', 'refunds', 'copyright'].map(key => [key, { enUS: '', zhCN: '' }])),
      } })
      if (path === '/api/v1/auth/oauth/providers') return route.fulfill({ json: { items: [] } })
      if (path === '/api/v1/notifications') return route.fulfill({ json: { items: [], unreadCount: 0 } })
      if (path === '/api/v1/auth/unified/start') return route.fulfill({ json: { email: user.email, accountExists: method !== 'registration', challenge: method === 'registration' ? challenge : undefined } })
      if (path === '/api/v1/auth/unified/send-code') return route.fulfill({ json: { challenge } })
      const authPath = method === 'password' ? '/api/v1/auth/login' : `/api/v1/auth/unified/${method === 'code' ? 'login-code' : 'register'}`
      if (path === authPath && route.request().method() === 'POST') {
        authWrites++
        authenticated = true
        return route.fulfill({ json: { user, authentication: 'session' } })
      }
      if (path === '/api/v1/orders' && authenticated) {
        orderReads++
        return route.fulfill({ json: { items: [] } })
      }
      unexpected.push(`${route.request().method()} ${path}`)
      return route.fulfill({ status: 500, json: { error: { code: 'unexpected_response', message: 'Unmocked request', retryable: false } } })
    })
    await page.goto('/auth?returnTo=%2Fworkspace%2Forders')
    await page.getByLabel('Email', { exact: true }).fill(user.email)
    await page.getByRole('button', { name: 'Next', exact: true }).click()
    if (method === 'code') await page.getByRole('button', { name: 'Use an email code instead', exact: true }).click()
    if (method !== 'password') await page.getByLabel('Email verification code', { exact: true }).fill('123456')
    if (method === 'registration') await page.getByLabel('Handle', { exact: true }).fill(user.handle)
    if (method !== 'code') await page.getByLabel(/^Password/).fill('fixture-password-2026')
    await page.locator('.account-form').getByRole('button', { name: method === 'password' ? 'Sign in' : method === 'code' ? 'Verify and sign in' : 'Create account', exact: true }).click()
    await expect(page).toHaveURL(/\/workspace\/orders$/)
    await expect(page.locator('.orders-workspace')).toBeVisible()
    await expect.poll(() => orderReads).toBe(1)
    expect(authWrites).toBe(1)
    expect(unexpected).toEqual([])
  })
}
