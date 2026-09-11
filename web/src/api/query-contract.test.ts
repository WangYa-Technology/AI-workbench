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

  it('retains the task filter convention for false and true', async () => {
    await api.listTasks({ mine: false, q: '' })
    await api.listTasks({ mine: true, q: 'a b' })
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/v1/tasks', '/api/v1/tasks?mine=true&q=a+b',
    ])
  })

  it('retains specialized search and truthy pagination conventions', async () => {
    await api.search({ q: '', types: ['image', 'video'], page: 0, limit: 0 })
    await api.listAccountSessions({ limit: 0, cursor: '' })
    await api.listProducts({ q: '', type: undefined, sort: 'recent' })
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/v1/search?q=&types=image%2Cvideo', '/api/v1/account/sessions', '/api/v1/products?sort=recent',
    ])
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
