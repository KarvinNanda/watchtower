import { describe, it, expect, beforeEach, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

// The stores' API modules pull in the axios client (browser-only); these
// tests never call the API.
vi.mock('@/api/market', () => ({}))
vi.mock('@/api/subscriptions', () => ({}))

import { useMarketStore } from './market'
import { useSubscriptionsStore } from './subscriptions'
import { resetUserData } from './userData'

// After logout (or an expired session) the next user on the same browser
// must not see the previous user's symbols or rules (#53).
describe('resetUserData', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('clears the market snapshot and stops its auto refresh', () => {
    const market = useMarketStore()
    market.snapshot = [{ symbol: 'BTC', price_usd: 1 }]
    market.lastUpdated = new Date()
    market.startAutoRefresh()

    resetUserData()

    expect(market.snapshot).toEqual([])
    expect(market.lastUpdated).toBeNull()
    expect(market.autoRefreshing).toBe(false)
  })

  it('clears asset rules and keywords', () => {
    const subs = useSubscriptionsStore()
    subs.assets = [{ id: 1, asset_symbol: 'BTC' }]
    subs.assetIntervalHours = 4
    subs.keywords = [{ id: 2, keyword: 'nginx' }]

    resetUserData()

    expect(subs.assets).toEqual([])
    expect(subs.assetIntervalHours).toBeNull()
    expect(subs.keywords).toEqual([])
  })
})
