import { describe, it, expect } from 'vitest'
import {
  formatPct,
  buildRulePayload,
  validateRuleForm,
  ruleToForm,
  ruleSummary,
  ruleState,
  sortRulesBySymbol,
  formatUSD,
  formatIDR,
  emptyRuleForm,
} from './assetRules'

const t = (key, params) => (params ? `${key}|${JSON.stringify(params)}` : key)
const base = { ...emptyRuleForm(), assetType: 'crypto', assetSymbol: 'BTC' }

describe('buildRulePayload', () => {
  it('sends intent without direction for a labelled level', () => {
    expect(buildRulePayload({ ...base, ruleType: 'price_level', intent: 'take_profit', direction: 'below', levelUsd: 120000 }))
      .toEqual({ asset_type: 'crypto', asset_symbol: 'BTC', rule_type: 'price_level', intent: 'take_profit', level_usd: 120000 })
  })
  it('sends direction for a plain level', () => {
    expect(buildRulePayload({ ...base, ruleType: 'price_level', intent: 'none', direction: 'above', levelUsd: 1 }))
      .toEqual({ asset_type: 'crypto', asset_symbol: 'BTC', rule_type: 'price_level', direction: 'above', level_usd: 1 })
  })
  it('sends only pct for pct rules, even if other fields are filled', () => {
    expect(buildRulePayload({ ...base, ruleType: 'trailing_stop', pct: 10, levelUsd: 5, direction: 'above' }))
      .toEqual({ asset_type: 'crypto', asset_symbol: 'BTC', rule_type: 'trailing_stop', pct: 10 })
  })
  it('sends only direction for week52', () => {
    expect(buildRulePayload({ ...base, ruleType: 'week52_break', direction: 'below', pct: 3 }))
      .toEqual({ asset_type: 'crypto', asset_symbol: 'BTC', rule_type: 'week52_break', direction: 'below' })
  })
})

describe('validateRuleForm', () => {
  it.each([
    [{ ruleType: 'price_level', intent: 'none', direction: null, levelUsd: 1 }, 'asset.rules.error_direction'],
    [{ ruleType: 'price_level', intent: 'stop_loss', levelUsd: 0 }, 'asset.rules.error_level'],
    [{ ruleType: 'pct_change_24h', pct: 100 }, ''],
    [{ ruleType: 'pct_change_24h', pct: 100.5 }, 'asset.rules.error_pct'],
    [{ ruleType: 'trailing_stop', pct: 100 }, 'asset.rules.error_pct_trailing'],
    [{ ruleType: 'trailing_stop', pct: 0.001 }, 'asset.rules.error_pct_trailing'],
    [{ ruleType: 'pct_change_24h', pct: 0.01 }, ''],
    [{ ruleType: 'week52_break', direction: 'above' }, ''],
    [{ ruleType: null }, 'common.required_field'],
  ])('%o -> %s', (form, key) => {
    expect(validateRuleForm({ ...base, ...form })).toBe(key)
  })
})

describe('ruleToForm', () => {
  it('maps a null intent to "none" and keeps the direction', () => {
    expect(ruleToForm({ asset_type: 'crypto', asset_symbol: 'BTC', rule_type: 'price_level', intent: null, direction: 'above', level_usd: 5, pct: null }))
      .toMatchObject({ intent: 'none', direction: 'above', levelUsd: 5 })
  })
})

describe('ruleSummary and ruleState', () => {
  it('describes a labelled level', () => {
    expect(ruleSummary({ rule_type: 'price_level', intent: 'take_profit', level_usd: 120000 }, t))
      .toBe('asset.rules.summary_intent|{"intent":"asset.rules.intent_take_profit","level":"$120,000.00"}')
  })
  it('marks a fired trailing stop', () => {
    expect(ruleState({ rule_type: 'trailing_stop', triggered_at: '2026-10-15T04:00:00Z', is_active: false })).toBe('fired')
    expect(ruleState({ rule_type: 'price_level', triggered_at: null, is_active: false })).toBe('paused')
  })
  it('marks a rule on a symbol WatchTower cannot fetch', () => {
    expect(ruleState({ rule_type: 'price_level', is_active: true, supported: false })).toBe('unsupported')
    expect(ruleState({ rule_type: 'price_level', is_active: true, supported: true })).toBe('active')
  })
})

describe('formatUSD', () => {
  it('keeps small crypto prices readable', () => {
    expect(formatUSD(0.00001234)).toBe('$0.00001234')
    expect(formatUSD(61633)).toBe('$61,633.00')
    expect(formatUSD(null)).toBe('—')
  })
})

describe('formatIDR', () => {
  it('keeps small crypto prices readable in rupiah', () => {
    expect(formatIDR(0.19663)).toContain('0,1966')
    expect(formatIDR(2392990)).toContain('2.392.990')
    expect(formatIDR(2392990)).not.toContain(',')
  })
})

describe('sortRulesBySymbol', () => {
  it('groups by symbol, then by id', () => {
    const sorted = sortRulesBySymbol([{ id: 3, asset_symbol: 'ETH' }, { id: 2, asset_symbol: 'BTC' }, { id: 1, asset_symbol: 'BTC' }])
    expect(sorted.map((r) => r.id)).toEqual([1, 2, 3])
  })
})

describe('formatPct', () => {
  it('signs and rounds to one decimal', () => {
    expect(formatPct(14.2857)).toBe('+14.3%')
    expect(formatPct(-2.4)).toBe('-2.4%')
    expect(formatPct(0)).toBe('0.0%')
  })

  it('null is a dash', () => {
    expect(formatPct(null)).toBe('—')
  })
})
