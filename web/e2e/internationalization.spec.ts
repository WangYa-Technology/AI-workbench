import { expect, test } from '@playwright/test'

test('keeps the Chinese core workflow localized and within mobile and desktop viewports', async ({ page }) => {
  const session = await page.request.post('/api/v1/auth/demo', { data: { actor: 'creator' } })
  expect(session.ok()).toBeTruthy()
  const consoleIssues: string[] = []
  page.on('console', (message) => {
    if (['warning', 'error'].includes(message.type())) consoleIssues.push(message.text())
  })

  await page.goto('/discover')
  await page.evaluate(() => globalThis.localStorage.setItem('hcai-locale', 'zh-CN'))
  const routes = [
    ['/create/image', '创作图片'],
    ['/workspace/assets', '资产'],
    ['/publish', '发布作品'],
    ['/market', '数字产品市场'],
    ['/market/demands', '任务广场'],
    ['/community', '社区'],
    ['/search?q=architectural', '搜索创作网络'],
  ] as const

  await page.setViewportSize({ width: 390, height: 844 })
  for (const [path, heading] of routes) {
    await page.goto(path)
    await expect(page.getByRole('heading', { name: heading, exact: true }).first()).toBeVisible()
    expect(await page.locator('html').getAttribute('lang')).toBe('zh-CN')
    const layout = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth, body: document.body.innerText }))
    expect(layout.scroll).toBe(layout.client)
    expect(layout.body).not.toMatch(/(?:^|\s)(?:accessibility|actions|admin|community|create|discover|marketplace|nav|notifications|publish|search|status|tasks|workspace)\.[A-Za-z][\w.]+/)
  }

  const adminSession = await page.request.post('/api/v1/auth/demo', { data: { actor: 'admin' } })
  expect(adminSession.ok()).toBeTruthy()
  await page.setViewportSize({ width: 1600, height: 1000 })
  await page.goto('/admin')
  await expect(page.getByRole('heading', { name: '运营管理', exact: true })).toBeVisible()
  const adminCopy = await page.locator('main').innerText()
  expect(adminCopy).not.toMatch(/\b(?:active|published|fulfilled|test_refunded|accepted|assigned|cancelled|disputed|enabled|disabled)\b/)
  await expect(page.locator('main')).toContainText('已发布')
  await expect(page.locator('main')).toContainText('已启用')
  const adminLayout = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
  expect(adminLayout.scroll).toBe(adminLayout.client)
  expect(consoleIssues).toEqual([])
})

test('localizes stable API failures instead of exposing English server copy', async ({ page }) => {
  await page.goto('/discover')
  await page.evaluate(() => globalThis.localStorage.setItem('hcai-locale', 'zh-CN'))
  await page.request.post('/api/v1/auth/logout')
  await page.goto('/settings')

  const form = page.locator('.auth-panel .account-form')
  await form.getByLabel('邮箱', { exact: true }).fill('missing-user@example.com')
  await form.getByLabel('密码', { exact: true }).fill('not-the-right-password')
  await form.getByRole('button', { name: '登录', exact: true }).click()

  await expect(page.getByRole('alert')).toContainText('邮箱或密码不正确。')
  await expect(page.getByRole('alert')).not.toContainText('The email or password is incorrect.')
})
