import { expect, test } from '@playwright/test'
import { readFile, readdir } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const e2eMediaRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../data/e2e-media')

async function newestEmail(userId: string, subject: string) {
  const directory = path.join(e2eMediaRoot, 'mailbox', userId)
  let result = ''
  await expect.poll(async () => {
    try {
      const names = await readdir(directory)
      const messages = await Promise.all(names.filter(name => name.endsWith('.eml')).map(async name => readFile(path.join(directory, name), 'utf8')))
      result = messages.reverse().find(message => message.includes(`Subject: ${subject}`)) || ''
      return Boolean(result)
    } catch {
      return false
    }
  }, { timeout: 60_000 }).toBe(true)
  return result
}

function actionURL(message: string) {
  const match = message.match(/https?:\/\/[^\s]+/)
  if (!match) throw new Error('Identity email did not include an action URL')
  return match[0]
}

test('email verification and password reset form a durable local identity loop', async ({ page, request }) => {
  test.setTimeout(120_000)
  const suffix = crypto.randomUUID().slice(0, 8)
  const email = `email-e2e-${suffix}@test.local`
  const handle = `email_${suffix}`
  const initialPassword = 'initial-password-2026'
  const replacementPassword = 'replacement-password-2026'

  const registered = await page.request.post('/api/v1/auth/register', { data: {
    email, handle, password: initialPassword, displayName: 'Email E2E Creator', locale: 'en-US', timezone: 'UTC',
  } })
  expect(registered.status()).toBe(201)

  const session = await request.get('/api/v1/auth/session', { headers: { Cookie: (await page.context().cookies()).map(cookie => `${cookie.name}=${cookie.value}`).join('; ') } })
  expect(session.ok()).toBeTruthy()
  const sessionBody = await session.json() as { user: { id: string } }
  const verificationMail = await newestEmail(sessionBody.user.id, 'Verify your HCAI CHAT email')
  await page.goto(actionURL(verificationMail))
  await page.getByRole('button', { name: 'Verify email' }).click()
  await expect(page.getByRole('heading', { name: 'Email verified' })).toBeVisible()
  await page.getByRole('link', { name: 'Open security settings' }).click()
  await expect(page.getByText('Verified', { exact: true }).first()).toBeVisible()

  await page.getByRole('button', { name: 'Sign out' }).first().click()
  await page.goto('/auth')
  await page.getByLabel('Email', { exact: true }).fill(email)
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await page.getByRole('button', { name: 'Forgot password?' }).click()
  await page.getByLabel('Email').fill(email)
  await page.getByRole('button', { name: 'Send reset link' }).click()
  await expect(page.getByText('If an active account uses that email, a reset message has been queued.')).toBeVisible()

  const resetMail = await newestEmail(sessionBody.user.id, 'Reset your HCAI CHAT password')
  await page.goto(actionURL(resetMail))
  await page.locator('input[autocomplete="new-password"]').first().fill(replacementPassword)
  await page.getByLabel('Confirm new password').fill(replacementPassword)
  await page.getByRole('button', { name: 'Reset password' }).click()
  await expect(page.getByRole('heading', { name: 'Password reset complete' })).toBeVisible()
  await page.getByRole('link', { name: 'Sign in' }).click()
  await page.getByLabel('Email').fill(email)
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await page.getByLabel('Password', { exact: true }).fill(replacementPassword)
  await page.getByRole('button', { name: 'Sign in' }).last().click()
  await expect(page).toHaveURL(/\/settings$/)
})
