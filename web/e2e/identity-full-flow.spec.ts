import { expect, test, type Page } from '@playwright/test'
import { readFile, readdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const projectRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const mailboxRoot = path.join(projectRoot, 'data/e2e-media/mailbox')
// Opt-in manual SMTP verification. Inputs are private files; no codes/tokens are logged.
const liveInbox = process.env.HCAI_LIVE_AUTH_EMAIL
const liveInputDir = process.env.HCAI_LIVE_AUTH_INPUT_DIR

async function mailInput(stage: string, localRead: () => Promise<string>) {
  if (liveInbox && !liveInputDir) throw new Error('Live verification requires a private input directory')
  if (liveInbox) console.log(`AUTH_MAIL_WAIT ${stage}`)
  let value = ''
  await expect.poll(async () => {
    value = await (liveInbox
      ? readFile(path.join(liveInputDir!, `${stage}.txt`), 'utf8')
      : localRead()).catch(() => '')
    return value.trim().length > 0
  }, { timeout: liveInbox ? 540_000 : 60_000, intervals: [1000, 2000] }).toBe(true)
  return value.trim()
}

async function readCode(stage: string, challenge: string) {
  return mailInput(stage, async () => {
    const body = await readFile(path.join(mailboxRoot, 'auth-challenges', `${challenge}.eml`), 'utf8')
    return body.match(/(?:code|验证码): (\d{6})/i)?.[1] || ''
  })
}

async function emailStep(page: Page, email: string) {
  await page.goto('/auth?returnTo=/workspace/assets')
  await page.getByLabel('Email', { exact: true }).fill(email)
  const pending = page.waitForResponse(r => r.url().endsWith('/auth/unified/start') && r.request().method() === 'POST')
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  const response = await pending
  expect(response.ok()).toBeTruthy()
  return response.json()
}

async function signOut(page: Page) {
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Sign out', exact: true }).first().click()
  await expect.poll(async () => (await page.request.get('/api/v1/auth/session')).status()).toBe(401)
}

test('registers with email, rejects invalid credentials, restores sessions, signs in by code and resets password', async ({ page, browser, baseURL }) => {
  test.setTimeout(liveInbox ? 1_800_000 : 120_000)
  page.setDefaultTimeout(15_000)
  const handle = `auth_${crypto.randomUUID().slice(0, 8)}`
  const email = liveInbox || `${handle}@example.test`
  const password = `Auth-${crypto.randomUUID()}-26`
  const replacement = `Reset-${crypto.randomUUID()}-26`
  const browserErrors: string[] = []
  page.on('pageerror', error => browserErrors.push(error.message))
  page.on('console', message => {
    if (message.type() === 'warning' && /Failed to resolve component/.test(message.text())) browserErrors.push(message.text())
  })

  const start = await emailStep(page, email)
  expect(start.accountExists).toBe(false)
  const code = await readCode('registration-code', start.challenge.challengeId)
  expect(/^\d{6}$/.test(code)).toBe(true)
  await page.getByLabel('Handle', { exact: true }).fill(handle)
  await page.locator('input[autocomplete="new-password"]').fill(password)
  await page.getByLabel('Email verification code', { exact: true }).fill(`${(Number(code[0]) + 1) % 10}${code.slice(1)}`)
  const wrongCode = page.waitForResponse(r => r.url().endsWith('/auth/unified/register'))
  await page.getByRole('button', { name: 'Create account', exact: true }).click()
  expect((await wrongCode).status()).toBe(422)
  expect((await page.request.get('/api/v1/auth/session')).status()).toBe(401)

  await page.getByLabel('Email verification code', { exact: true }).fill(code)
  const registration = page.waitForResponse(r => r.url().endsWith('/auth/unified/register'))
  await page.getByRole('button', { name: 'Create account', exact: true }).click()
  const registered = await registration
  expect(registered.status()).toBe(201)
  const { user } = await registered.json()
  expect(user.emailVerified).toBe(true)
  expect(user.email).toBe(email)
  if (liveInputDir) await writeFile(path.join(liveInputDir, 'created-user.json'), JSON.stringify({ id: user.id, email, handle }), { mode: 0o600 })
  await expect(page).toHaveURL(url => url.pathname === '/workspace/assets')
  const cookie = (await page.context().cookies()).find(c => c.name === 'hcai_session')!
  expect(cookie.httpOnly).toBe(true)
  expect(cookie.sameSite).toBe('Lax')
  await page.reload()
  expect((await (await page.request.get('/api/v1/auth/session')).json()).user.id).toBe(user.id)
  await signOut(page)
  const revoked = await page.request.get('/api/v1/auth/session', { headers: { Cookie: `hcai_session=${cookie.value}` } })
  expect(revoked.status()).toBe(401)
  console.log('AUTH_CHECK registration, invalid-code, verified-email, persistence, logout-revocation passed')

  expect((await emailStep(page, email)).accountExists).toBe(true)
  await page.getByLabel('Password', { exact: true }).fill('wrong-password-2026')
  const wrongPassword = page.waitForResponse(r => r.url().endsWith('/auth/login'))
  await page.locator('.account-form button[type="submit"]').click()
  expect((await wrongPassword).status()).toBe(401)
  await expect(page.getByRole('alert')).toBeVisible()
  await page.getByLabel('Password', { exact: true }).fill(password)
  await page.locator('.account-form button[type="submit"]').click()
  await expect(page).toHaveURL(url => url.pathname === '/workspace/assets')
  const otherSession = await browser.newContext({ baseURL })
  expect((await otherSession.request.post('/api/v1/auth/login', { data: { email, password } })).status()).toBe(200)
  await signOut(page)
  console.log('AUTH_CHECK password login and invalid-password rejection passed')

  await emailStep(page, email)
  const sent = page.waitForResponse(r => r.url().endsWith('/auth/unified/send-code'))
  await page.getByRole('button', { name: 'Use an email code instead', exact: true }).click()
  const challenge = (await (await sent).json()).challenge
  const loginCode = await readCode('login-code', challenge.challengeId)
  await page.getByLabel('Email verification code', { exact: true }).fill(loginCode)
  const confirmed = page.waitForResponse(r => r.url().endsWith('/auth/unified/login-code'))
  await page.getByRole('button', { name: 'Verify and sign in', exact: true }).click()
  expect((await confirmed).status()).toBe(200)
  await expect(page).toHaveURL(url => url.pathname === '/workspace/assets')
  await signOut(page)
  const replay = await page.request.post('/api/v1/auth/unified/login-code', { data: { email, code: loginCode, challengeId: challenge.challengeId } })
  expect(replay.status()).toBe(409)
  expect((await page.request.get('/api/v1/auth/session')).status()).toBe(401)
  console.log('AUTH_CHECK email-code login and one-time-code replay rejection passed')

  await emailStep(page, email)
  await page.getByRole('button', { name: 'Forgot password?', exact: true }).click()
  await page.getByRole('button', { name: 'Send reset link', exact: true }).click()
  await expect(page.getByText('If an active account uses that email, a reset message has been queued.')).toBeVisible()
  const resetURL = await mailInput('reset-link', async () => {
    const directory = path.join(mailboxRoot, user.id)
    for (const file of await readdir(directory)) {
      const body = await readFile(path.join(directory, file), 'utf8')
      if (body.includes('Subject: Reset your HCAI CHAT password')) return body.match(/https?:\/\/[^\s]+/)?.[0] || ''
    }
    return ''
  })
  // The URL may contain a token: never include it in logs or assertion messages.
  const link = new URL(resetURL)
  expect(link.origin === new URL(baseURL!).origin).toBe(true)
  await page.goto(resetURL)
  await page.locator('input[autocomplete="new-password"]').first().fill(replacement)
  await page.getByLabel('Confirm new password', { exact: true }).fill(replacement)
  await page.getByRole('button', { name: 'Reset password', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Password reset complete', exact: true })).toBeVisible()
  expect((await otherSession.request.get('/api/v1/auth/session')).status()).toBe(401)
  await otherSession.close()
  expect((await page.request.post('/api/v1/auth/login', { data: { email, password } })).status()).toBe(401)
  await emailStep(page, email)
  await page.getByLabel('Password', { exact: true }).fill(replacement)
  await page.locator('.account-form button[type="submit"]').click()
  await expect(page).toHaveURL(url => url.pathname === '/workspace/assets')
  expect((await (await page.request.get('/api/v1/auth/session')).json()).user.id).toBe(user.id)
  await signOut(page)
  expect(browserErrors).toEqual([])
  console.log('AUTH_CHECK password reset, all-session revocation, old-password rejection and new-password login passed')
})
