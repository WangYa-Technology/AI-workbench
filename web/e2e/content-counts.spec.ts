import { fixtureCredentials } from './helpers/identity'
import { expect, test } from '@playwright/test'

test('community counts ignore pagination/category but respect search and mine', async ({ page }) => {
  const token = `count-${Date.now()}`
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  const directory = await (await page.request.get('/api/v1/task-types?scope=community')).json()
  const categories = directory.items.slice(0, 2).map((item: { code: string }) => item.code)
  expect(categories).toHaveLength(2)
  for (const category of categories) {
    const created = await page.request.post('/api/v1/community/posts', { data: { category, title: `${token} discussion`, body: 'A concrete discussion for category counting and pagination verification.' } })
    expect(created.ok()).toBeTruthy()
  }
  const first = await (await page.request.get(`/api/v1/community/posts?q=${token}&mine=true&limit=1`)).json()
  expect(first.items).toHaveLength(1)
  expect(first.categoryCounts).toEqual(Object.fromEntries(categories.map((category: string) => [category, 1])))
  const filtered = await (await page.request.get(`/api/v1/community/posts?q=${token}&mine=true&category=${categories[0]}`)).json()
  expect(filtered.items).toHaveLength(1)
  expect(filtered.categoryCounts).toEqual(first.categoryCounts)
  const next = await (await page.request.get(`/api/v1/community/posts?q=${token}&mine=true&limit=1&cursor=${encodeURIComponent(first.nextCursor)}`)).json()
  expect(next.categoryCounts).toEqual(first.categoryCounts)
  expect(next.items[0].id).not.toBe(first.items[0].id)
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('publisher') })
  const mine = await (await page.request.get(`/api/v1/community/posts?q=${token}&mine=true`)).json()
  expect(mine.items).toEqual([])
  expect(mine.categoryCounts).toEqual({})
})

test('market category counts stay stable across category selection and narrow with search', async ({ page }) => {
  const all = await (await page.request.get('/api/v1/products')).json()
  expect(all.items.length).toBeGreaterThan(1)
  const product = all.items[0]
  const filtered = await (await page.request.get(`/api/v1/products?category=${product.category}`)).json()
  expect(filtered.categoryCounts).toEqual(all.categoryCounts)
  const search = await (await page.request.get(`/api/v1/products?q=${encodeURIComponent(product.title)}`)).json()
  expect(Object.values(search.categoryCounts).reduce((sum: number, n) => sum + Number(n), 0)).toBe(search.items.length)
  expect(search.items.some((item: { id: string }) => item.id === product.id)).toBe(true)
  const empty = await (await page.request.get('/api/v1/products?q=nonexistent-audit-product-74298')).json()
  expect(empty.items).toEqual([])
  expect(empty.categoryCounts).toEqual({})
})

test('discussed order includes older posts and preserves its cursor and return context', async ({ page }) => {
  const token = `rank-${Date.now()}`
  await page.request.post('/api/v1/auth/login', { data: fixtureCredentials('creator') })
  const directory = await (await page.request.get('/api/v1/task-types?scope=community')).json()
  const ids: string[] = []
  for (const suffix of ['older', 'newer']) {
    const response = await page.request.post('/api/v1/community/posts', { data: { category: directory.items[0].code, title: `${token} ${suffix}`, body: 'Testing discussion ranking across page boundaries with real published posts.' } })
    expect(response.ok()).toBeTruthy()
    ids.push((await response.json()).id)
  }
  expect((await page.request.post(`/api/v1/community/posts/${ids[0]}/comments`, { data: { body: 'This older discussion has a useful reply.' } })).ok()).toBeTruthy()
  const latest = await (await page.request.get(`/api/v1/community/posts?q=${token}&limit=1`)).json()
  expect(latest.items[0].id).toBe(ids[1])
  const discussed = await (await page.request.get(`/api/v1/community/posts?q=${token}&sort=discussed&limit=1`)).json()
  expect(discussed.items[0].id).toBe(ids[0])
  const next = await (await page.request.get(`/api/v1/community/posts?q=${token}&sort=discussed&limit=1&cursor=${encodeURIComponent(discussed.nextCursor)}`)).json()
  expect(next.items[0].id).toBe(ids[1])
  expect((await page.request.get(`/api/v1/community/posts?sort=latest&cursor=${encodeURIComponent(discussed.nextCursor)}`)).status()).toBe(422)
  await page.goto(`/community?q=${token}&sort=discussed`)
  await expect(page.getByRole('tab', { name: 'Most discussed', exact: true })).toHaveAttribute('aria-selected', 'true')
  await page.getByRole('heading', { name: `${token} older`, exact: true }).click()
  await page.locator('.community-post-back').click()
  await expect(page).toHaveURL(new RegExp(`q=${token}.*sort=discussed`))
})

test('community keeps its sidebar and ignores a late search response', async ({ page }) => {
  await page.goto('/community')
  const initial = await (await page.request.get('/api/v1/community/posts')).json()
  const post = initial.items[0]
  const sidebar = await page.locator('.category-sidebar').elementHandle()
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/v1/community/posts?*', async route => {
    const q = new URL(route.request().url()).searchParams.get('q')
    if (q === 'slow') await gate
    if (q === 'slow' || q === 'fast') return route.fulfill({ json: { items: [{ ...post, title: q === 'slow' ? 'Slow result' : 'Fast result' }], categoryCounts: initial.categoryCounts } })
    return route.continue()
  })
  const search = page.getByRole('searchbox', { name: 'Search discussions', exact: true })
  const pending = page.waitForRequest(request => new URL(request.url()).searchParams.get('q') === 'slow')
  await search.fill('slow')
  await pending
  expect(await sidebar!.evaluate(node => node.isConnected)).toBe(true)
  await search.fill('fast')
  await expect(page.getByRole('heading', { name: 'Fast result', exact: true })).toBeVisible()
  const stale = page.waitForResponse(response => new URL(response.url()).searchParams.get('q') === 'slow')
  release()
  await stale
  await expect(page.getByRole('heading', { name: 'Slow result', exact: true })).toHaveCount(0)
  expect(await sidebar!.evaluate(node => node.isConnected)).toBe(true)
})
