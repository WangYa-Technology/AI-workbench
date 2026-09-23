import { expect, test } from '@playwright/test'

test('registration and reset reject oversized UTF-8 passwords as validation errors', async ({ request }) => {
  const handle = `password_${Date.now().toString(36)}`
  const user = { email: `${handle}@example.test`, handle, displayName: 'Password Boundary', locale: 'en-US', timezone: 'UTC' }
  for (const password of ['a'.repeat(73), '密'.repeat(25), '🔑'.repeat(19)]) {
    expect((await request.post('/api/v1/auth/register', { data: { ...user, password } })).status()).toBe(422)
    expect((await request.post('/api/v1/auth/password-reset-confirm', { data: { token: 'invalid-token', password } })).status()).toBe(422)
  }
  const password = '密'.repeat(24)
  expect((await request.post('/api/v1/auth/register', { data: { ...user, password } })).status()).toBe(201)
  await request.post('/api/v1/auth/logout')
  expect((await request.post('/api/v1/auth/login', { data: { email: user.email, password } })).status()).toBe(200)
})

test('reset form explains the byte limit before making a request', async ({ page }) => {
  let resets = 0
  page.on('request', r => { if (r.url().endsWith('/auth/password-reset-confirm')) resets++ })
  await page.goto('/reset-password?token=invalid-token')
  await page.locator('input[autocomplete="new-password"]').nth(0).fill('密'.repeat(25))
  await page.locator('input[autocomplete="new-password"]').nth(1).fill('密'.repeat(25))
  await page.locator('.account-form button[type="submit"]').click()
  await expect(page.getByRole('alert')).toContainText('72 UTF-8 bytes')
  expect(resets).toBe(0)
})
