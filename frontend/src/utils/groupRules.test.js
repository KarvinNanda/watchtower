import { describe, it, expect } from 'vitest'
import { groupRulesBySymbol } from './groupRules'

const rule = (id, symbol, extra = {}) => ({ id, asset_symbol: symbol, asset_type: 'crypto', is_active: true, ...extra })
const btc = { symbol: 'BTC', price_usd: 61240 }

describe('groupRulesBySymbol', () => {
  it('groups by symbol, symbols sorted, rules by id', () => {
    const groups = groupRulesBySymbol([rule(5, 'ETH'), rule(3, 'BTC'), rule(1, 'BTC')], [btc])
    expect(groups.map((g) => g.symbol)).toEqual(['BTC', 'ETH'])
    expect(groups[0].rules.map((r) => r.id)).toEqual([1, 3])
    expect(groups[0].assetType).toBe('crypto')
  })

  it('attaches the market row, or null when there is none', () => {
    const groups = groupRulesBySymbol([rule(1, 'BTC'), rule(2, 'ANTAM', { asset_type: 'gold' })], [btc])
    expect(groups.find((g) => g.symbol === 'BTC').market).toBe(btc)
    expect(groups.find((g) => g.symbol === 'ANTAM').market).toBeNull()
  })

  it('keeps paused and unsupported rules', () => {
    const groups = groupRulesBySymbol([rule(1, 'BTC', { is_active: false }), rule(2, 'BTC', { supported: false })], [btc])
    expect(groups[0].rules).toHaveLength(2)
  })

  it('no rules → no groups', () => {
    expect(groupRulesBySymbol([], [])).toEqual([])
  })

  it('snapshot failed (null) → rules still grouped, market null', () => {
    const groups = groupRulesBySymbol([rule(1, 'BTC')], null)
    expect(groups).toHaveLength(1)
    expect(groups[0].market).toBeNull()
  })
})
