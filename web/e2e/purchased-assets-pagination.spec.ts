import { expect, test } from '@playwright/test'
import { fixtureCredentials } from './helpers/identity'

test('purchased assets request filtered pages and ignore a continuation after leaving the view', async ({ page }) => {
  const login = await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  expect(login.ok()).toBeTruthy()
  const original = await (await page.request.get('/api/v1/assets')).json()
  expect(original.items.length).toBeGreaterThan(0)
  const asset = original.items[0]
  const first = { ...asset, id: '00000000-0000-4000-8000-000000009901', title: 'First purchased asset', sourceType: 'purchase' }
  const second = { ...asset, id: '00000000-0000-4000-8000-000000009902', title: 'Second purchased asset', sourceType: 'purchase' }
  let delayContinuation = false
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  await page.route(/\/api\/v1\/assets(?:\?.*)?$/, async route => {
    const params = new URL(route.request().url()).searchParams
    if (params.get('source') !== 'purchase') {
      await route.fulfill({ json: { items: [asset], total: 1 } })
      return
    }
    if (params.get('cursor')) {
      expect(params.get('cursor')).toBe('purchases-next-page')
      if (delayContinuation) await gate
      await route.fulfill({ json: { items: [second], total: 2 } })
    } else {
      await route.fulfill({ json: { items: [first], total: 2, nextCursor: 'purchases-next-page' } })
    }
  })
  await page.goto('/workspace/purchases')
  await expect(page.getByRole('heading', { name: first.title, exact: true })).toBeVisible()
  const count = page.locator('.catalog-toolbar [role=status]')
  await expect(count).toHaveText('2 Purchases')
  await expect(page.locator('.page-hero-stats, main [role=tablist]')).toHaveCount(0)
  await expect(page.getByRole('heading', { name: asset.title, exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await expect(page.getByRole('heading', { name: second.title, exact: true })).toBeVisible()
  await expect(page.locator('.asset-row')).toHaveCount(2)
  await expect(count).toHaveText('2 Purchases')
  await expect(page.getByRole('button', { name: 'Load more', exact: true })).toHaveCount(0)

  await page.reload()
  await expect(page.getByRole('heading', { name: first.title, exact: true })).toBeVisible()
  delayContinuation = true
  const nextRequest = page.waitForRequest(request => request.url().includes('cursor=purchases-next-page'))
  await page.getByRole('button', { name: 'Load more', exact: true }).click()
  await nextRequest
  await page.locator('a[href="/workspace/assets"]').first().click()
  await expect(page.getByRole('heading', { name: asset.title, exact: true })).toBeVisible()
  const nextResponse = page.waitForResponse(response => response.url().includes('cursor=purchases-next-page'))
  release()
  await nextResponse
  await expect(page.getByRole('heading', { name: second.title, exact: true })).toHaveCount(0)
  await expect(page.locator('.asset-row')).toHaveCount(1)
})

test('switching buyer through sign out clears the previous purchased page and cursor', async ({ page }) => {
  expect((await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })).ok()).toBeTruthy()
  const original = await (await page.request.get('/api/v1/assets')).json()
  const asset = { ...original.items[0], title: 'Previous buyer purchase', sourceType: 'purchase' }
  let switched = false
  await page.route(/\/api\/v1\/assets(?:\?.*)?$/, route => route.fulfill({ json: switched ? { items: [], total: 0 } : { items: [asset], total: 2, nextCursor: 'old-buyer-cursor' } }))
  await page.goto('/workspace/purchases')
  await expect(page.getByRole('heading',{name:asset.title,exact:true})).toBeVisible()
  await page.locator('a[href="/settings"]').first().click()
  await page.getByRole('button',{name:'Sign out',exact:true}).click()
  await expect(page.getByLabel('Email',{exact:true})).toBeVisible()
  switched = true
  await page.getByLabel('Email',{exact:true}).fill(fixtureCredentials('publisher').email)
  await page.locator('.account-form button[type="submit"]').click()
  await page.locator('input[autocomplete="current-password"]').fill(fixtureCredentials('publisher').password)
  await page.locator('.account-form button[type="submit"]').click()
  await expect(page.locator('input[autocomplete="current-password"]')).toHaveCount(0)
  await expect(page.locator('a[href="/workspace/purchases"]').first()).toBeVisible()
  await page.locator('a[href="/workspace/purchases"]').first().click()
  await expect(page.getByText('No licensed purchases',{exact:true})).toBeVisible()
  await expect(page.getByRole('heading',{name:asset.title,exact:true})).toHaveCount(0)
  await expect(page.getByRole('button',{name:'Load more',exact:true})).toHaveCount(0)
  const session = await (await page.request.get('/api/v1/auth/session')).json()
  expect(session.user.email).toBe(fixtureCredentials('publisher').email)
})
