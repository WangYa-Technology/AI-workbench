import { readFile } from 'node:fs/promises'
import { expect, type Page } from '@playwright/test'

export async function registerFromEmail(page: Page, handle: string, password = 'creator-password-2026') {
  await page.getByLabel('Email', { exact: true }).fill(`${handle}@example.test`)
  const start = page.waitForResponse(r => r.url().endsWith('/auth/unified/start') && r.request().method() === 'POST')
  await page.locator('.account-form button[type="submit"]').click()
  const response = await start
  expect(response.ok()).toBeTruthy()
  const { challenge } = await response.json()
  expect(challenge.challengeId).toMatch(/^[0-9a-f-]{36}$/)
  const mailbox = new URL(`../../../data/e2e-media/mailbox/auth-challenges/${challenge.challengeId}.eml`, import.meta.url)
  let code = ''
  await expect.poll(async () => {
    const mail = await readFile(mailbox, 'utf8').catch(() => '')
    code = mail.match(/(?:code|验证码): (\d{6})/i)?.[1] || ''
    return code.length
  }).toBe(6)
  await page.getByLabel('Email verification code', { exact: true }).fill(code)
  await page.getByLabel('Handle', { exact: true }).fill(handle)
  await page.locator('input[autocomplete="new-password"]').fill(password)
  await page.locator('.account-form button[type="submit"]').click()
}
