import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, APIError } from './client'

const listEndpoints = [
  ['listAccountEmailActions', '/account/email-actions'],
  ['listDataRightsRequests', '/account/data-rights'],
  ['listNotifications', '/notifications'],
  ['listSupportCases', '/support/cases'],
  ['listGenerations', '/generations'],
  ['billingStatement', '/billing/statement'],
  ['listMyCommunityReports', '/community/reports/mine'],
  ['adminListUsers', '/admin/users'],
  ['adminListContent', '/admin/content'],
  ['adminListMedia', '/admin/media'],
  ['adminListGenerations', '/admin/generations'],
  ['adminListTasks', '/admin/tasks'],
  ['adminListPayments', '/admin/payments'],
  ['adminListPaymentDestinations', '/admin/payment-destinations'],
  ['adminListFinance', '/admin/finance/accounts'],
  ['adminListProviderCostReconciliations', '/admin/provider-cost-reconciliations'],
  ['adminListRiskSignals', '/admin/risk/signals'],
  ['adminGetRiskRules', '/admin/risk/rules'],
  ['adminGetRankingPolicy', '/admin/discovery/ranking'],
  ['adminGetDiscoveryOperations', '/admin/discovery/operations'],
  ['adminListWebhookDeadLetters', '/admin/developer/webhooks/dead-letters'],
  ['adminListEmailActionDeadLetters', '/admin/email-actions/dead-letters'],
  ['adminListDataRights', '/admin/data-rights'],
  ['adminListDataRightsHolds', '/admin/data-rights/holds'],
  ['adminListMediaCleanups', '/admin/data-rights/media-cleanups'],
  ['adminListGovernanceReports', '/admin/governance/reports'],
  ['adminListGovernanceAppeals', '/admin/governance/appeals'],
  ['adminListSupportCases', '/admin/support/cases'],
] as const satisfies ReadonlyArray<readonly [keyof typeof api, string]>

describe('list request contracts', () => {
  const fetchMock = vi.fn<typeof fetch>()
  const page = { items: [], nextCursor: 'next-page' }

  beforeEach(() => {
    fetchMock.mockReset()
    fetchMock.mockImplementation(async () => new Response(JSON.stringify(page)))
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => vi.unstubAllGlobals())

  it('keeps refund evidence reads scoped, cancellable, and uncached through pagination', async () => {
    const controller = new AbortController()
    await api.adminRefundChecks('payment/id', 'unresolved', 'next/+= &', controller.signal)
    await api.adminRefundCheck('payment/id', 'check/id', controller.signal)
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/v1/admin/payments/payment%2Fid/refund-checks?review=unresolved&cursor=next%2F%2B%3D+%26',
      '/api/v1/admin/payments/payment%2Fid/refund-checks/check%2Fid',
    ])
    for (const [, init] of fetchMock.mock.calls) {
      expect(init?.signal).toBe(controller.signal)
      expect(init?.cache).toBe('no-store')
      expect(init?.credentials).toBe('include')
      expect(init?.method ?? 'GET').toBe('GET')
      expect(init?.body).toBeUndefined()
    }
    fetchMock.mockClear()
    await api.adminRefundChecks('payment/id', 'all')
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/admin/payments/payment%2Fid/refund-checks?review=all')
  })

  it('sends recovery bytes unchanged to the reserved target without JSON or multipart encoding', async () => {
    const file = new Blob([new Uint8Array([0, 255, 13, 10])], { type: 'image/jpeg' })
    const controller = new AbortController()
    await api.adminUploadDeliveryRepair('order/id', 'repair/id', file, controller.signal)
    const [url, init] = fetchMock.mock.calls[0]!
    expect(url).toBe('/api/v1/admin/product-deliveries/order%2Fid/repairs/repair%2Fid/content?confirmed=true')
    expect(init?.method).toBe('PUT')
    expect(init?.body).toBe(file)
    expect(new Headers(init?.headers).get('Content-Type')).toBe('application/octet-stream')
    expect(init?.signal).toBe(controller.signal)
    expect(init?.credentials).toBe('include')
  })


  it('preserves explicit invalid search pagination for server validation', async () => {
    await api.search({ q: 'reference', page: 0, limit: 0 })
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/search?q=reference&page=0&limit=0')
    await api.search({ q: 'reference', page: Number.NaN })
    expect(fetchMock.mock.calls[1]?.[0]).toBe('/api/v1/search?q=reference&page=NaN')
  })

  it.each(listEndpoints)('%s preserves the default URL and response', async (name, path) => {
    expect(await api[name]()).toEqual(page)
    expect(fetchMock).toHaveBeenCalledExactlyOnceWith(`/api/v1${path}`, {
      headers: new Headers(), credentials: 'include',
    })
  })

  it.each(listEndpoints)('%s preserves cursor encoding, zero and omitted fields', async (name, path) => {
    const query = Object.freeze({ cursor: 'a +/&=你好', limit: 0, status: undefined, q: '' })
    await api[name](query)
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      `/api/v1${path}?cursor=a+%2B%2F%26%3D%E4%BD%A0%E5%A5%BD&limit=0`,
    )
  })

  it('encodes webhook IDs separately from query values', async () => {
    await api.listDeveloperWebhookDeliveries('id/with +', { cursor: 'next/+', limit: 20 })
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      '/api/v1/account/developer-webhooks/id%2Fwith%20%2B/deliveries?cursor=next%2F%2B&limit=20',
    )
  })

  it('retains cleanup kind and status with pagination', async () => {
    await api.adminListMediaCleanups({ kind: 'product', status: 'failed', limit: 20, cursor: 'next/+' })
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/admin/data-rights/media-cleanups?kind=product&status=failed&limit=20&cursor=next%2F%2B')
  })

  it('retains the task filter convention for false and true', async () => {
    await api.listTasks({ mine: false, q: '' })
    await api.listTasks({ mine: true, q: 'a b' })
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/v1/tasks', '/api/v1/tasks?mine=true&q=a+b',
    ])
  })

  it('retains the purchase source on every asset page request', async () => {
    await api.listAssets({ source: 'purchase', limit: 20 })
    await api.listAssets({ source: 'purchase', cursor: 'next/+' })
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/v1/assets?source=purchase&limit=20', '/api/v1/assets?source=purchase&cursor=next%2F%2B',
    ])
  })

  it('retains preview purpose through pagination and leaves invalid sizes for server validation', async () => {
    await api.listAssets({ purpose: 'product_preview', limit: 20 })
    await api.listAssets({ purpose: 'product_preview', cursor: 'next/+' })
    await api.listAssets({ purpose: 'product_preview', limit: 0 })
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/v1/assets?purpose=product_preview&limit=20',
      '/api/v1/assets?purpose=product_preview&cursor=next%2F%2B',
      '/api/v1/assets?purpose=product_preview&limit=0',
    ])
  })

  it('retains an uncertain checkout key until verified expiry, then requires a new user request', async () => {
    const product = crypto.randomUUID()
    const body = { error: { code: 'payment_checkout_conflict', message: 'Still unresolved', retryable: false } }
    fetchMock.mockImplementation(async () => new Response(JSON.stringify(body), { status: 409 }))
    await expect(api.checkoutProduct(product, true, 'a'.repeat(64))).rejects.toMatchObject({ code: 'payment_checkout_conflict' })
    body.error.code = 'payment_reconciliation_required'
    await expect(api.checkoutProduct(product, true, 'a'.repeat(64))).rejects.toMatchObject({ code: 'payment_reconciliation_required' })
    body.error.code = 'payment_checkout_expired'
    await expect(api.checkoutProduct(product, true, 'a'.repeat(64))).rejects.toMatchObject({ code: 'payment_checkout_expired' })
    expect(fetchMock).toHaveBeenCalledTimes(3)
    fetchMock.mockImplementation(async () => new Response(JSON.stringify({ checkoutUrl: 'https://checkout.stripe.com/new' })))
    await api.checkoutProduct(product, true, 'a'.repeat(64))
    const keys = fetchMock.mock.calls.map(([, init]) => new Headers(init?.headers).get('Idempotency-Key'))
    expect(keys[0]).toBeTruthy()
    expect(keys[1]).toBe(keys[0])
    expect(keys[2]).toBe(keys[0])
    expect(keys[3]).not.toBe(keys[0])
  })

  it('retains a failed closure key, and clears only a definitively closed checkout key', async () => {
    const order = crypto.randomUUID()
    const failure = { error: { code: 'internal_error', message: 'Lost response', retryable: true } }
    fetchMock.mockImplementation(async () => new Response(JSON.stringify(failure), { status: 500 }))
    await expect(api.closeProductCheckout(order, 7)).rejects.toMatchObject({ status: 500 })
    fetchMock.mockImplementation(async () => new Response(JSON.stringify({ id: order, status: 'cancelled' })))
    await api.closeProductCheckout(order, 7)
    expect(fetchMock.mock.calls[0]?.[1]?.body).toBe(JSON.stringify({ expectedVersion: 7, confirmed: true }))
    const closureKeys = fetchMock.mock.calls.map(([, init]) => new Headers(init?.headers).get('Idempotency-Key'))
    expect(closureKeys[0]).toBeTruthy()
    expect(closureKeys[1]).toBe(closureKeys[0])
    fetchMock.mockClear()
    const product = crypto.randomUUID()
    failure.error.code = 'payment_checkout_preparation_failed'
    fetchMock.mockImplementation(async () => new Response(JSON.stringify(failure), { status: 409 }))
    await expect(api.checkoutProduct(product, true, 'a'.repeat(64))).rejects.toMatchObject({ code: failure.error.code })
    failure.error.code = 'payment_checkout_closed'
    await expect(api.checkoutProduct(product, true, 'a'.repeat(64))).rejects.toMatchObject({ code: failure.error.code })
    fetchMock.mockImplementation(async () => new Response(JSON.stringify({ checkoutUrl: 'https://checkout.stripe.com/new' })))
    await api.checkoutProduct(product, true, 'a'.repeat(64))
    const keys = fetchMock.mock.calls.map(([, init]) => new Headers(init?.headers).get('Idempotency-Key'))
    expect(keys[0]).toBeTruthy()
    expect(keys[1]).toBe(keys[0])
    expect(keys[2]).not.toBe(keys[0])
  })

  it("preserves search validation values and other endpoints' pagination conventions", async () => {
    await api.search({ q: '', types: ['image', 'video'], page: 0, limit: 0 })
    await api.listAccountSessions({ limit: 0, cursor: '' })
    await api.listProducts({ q: '', type: undefined, sort: 'recent' })
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/v1/search?q=&types=image%2Cvideo&page=0&limit=0', '/api/v1/account/sessions', '/api/v1/products?sort=recent',
    ])
  })

  it('retains all product filters and encoded cursors across pages', async () => {
    const filters = Object.freeze({ q: '100%_作品', type: 'work', category: 'market_work', license: 'hcai-standard', sort: 'price_asc', limit: 7 })
    await api.listProducts(filters)
    await api.listProducts({ ...filters, cursor: 'next/+' })
    const urls = fetchMock.mock.calls.map(([url]) => new URL(String(url), 'https://example.test'))
    for (const url of urls) {
      expect(url.pathname).toBe('/api/v1/products')
      for (const [key, value] of Object.entries(filters)) expect(url.searchParams.get(key)).toBe(String(value))
    }
    expect(urls[0]?.searchParams.has('cursor')).toBe(false)
    expect(urls[1]?.searchParams.get('cursor')).toBe('next/+')
  })

  it('preserves structured HTTP error evidence', async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ error: {
      code: 'invalid_cursor', message: 'Invalid cursor', retryable: false, requestId: 'req-1',
    } }), { status: 400 }))
    await expect(api.listGenerations({ cursor: 'bad' })).rejects.toMatchObject({
      name: 'APIError', status: 400, code: 'invalid_cursor', retryable: false, requestId: 'req-1',
    } satisfies Partial<APIError>)
  })
})
