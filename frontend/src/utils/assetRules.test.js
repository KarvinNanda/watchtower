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
  defaultCurrency,
  setCurrency,
  formatLevel,
  changeAssetType,
} from './assetRules'

const t = (key, params) => (params ? `${key}|${JSON.stringify(params)}` : key)
const base = { ...emptyRuleForm(), assetType: 'crypto', assetSymbol: 'BTC' }

describe('buildRulePayload', () => {
  it('sends intent without direction for a labelled level', () => {
    expect(buildRulePayload({ ...base, ruleType: 'price_level', intent: 'take_profit', direction: 'below', level: 120000 }))
      .toEqual({ asset_type: 'crypto', asset_symbol: 'BTC', rule_type: 'price_level', intent: 'take_profit', level_usd: 120000 })
  })
  it('sends direction for a plain level', () => {
    expect(buildRulePayload({ ...base, ruleType: 'price_level', intent: 'none', direction: 'above', level: 1 }))
      .toEqual({ asset_type: 'crypto', asset_symbol: 'BTC', rule_type: 'price_level', direction: 'above', level_usd: 1 })
  })
  it('sends only pct for pct rules, even if other fields are filled', () => {
    expect(buildRulePayload({ ...base, ruleType: 'trailing_stop', pct: 10, level: 5, direction: 'above' }))
      .toEqual({ asset_type: 'crypto', asset_symbol: 'BTC', rule_type: 'trailing_stop', pct: 10 })
  })
  it('sends only direction for week52', () => {
    expect(buildRulePayload({ ...base, ruleType: 'week52_break', direction: 'below', pct: 3 }))
      .toEqual({ asset_type: 'crypto', asset_symbol: 'BTC', rule_type: 'week52_break', direction: 'below' })
  })
})

describe('validateRuleForm', () => {
  it.each([
    [{ ruleType: 'price_level', intent: 'none', direction: null, level: 1 }, 'asset.rules.error_direction'],
    [{ ruleType: 'price_level', intent: 'stop_loss', level: 0 }, 'asset.rules.error_level'],
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
      .toMatchObject({ intent: 'none', direction: 'above', level: 5, currency: 'USD' })
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

describe('level currency', () => {
  it('defaults to IDR for gold and USD otherwise', () => {
    expect(defaultCurrency('gold')).toBe('IDR')
    expect(defaultCurrency('crypto')).toBe('USD')
    expect(defaultCurrency(null)).toBe('USD')
  })

  it('clears the level when the currency changes, keeps it otherwise', () => {
    const form = { ...base, ruleType: 'price_level', level: 120000, currency: 'USD' }
    expect(setCurrency(form, 'IDR')).toMatchObject({ currency: 'IDR', level: null })
    expect(setCurrency(form, 'USD')).toMatchObject({ currency: 'USD', level: 120000 })
  })

  it('sends level_idr for an IDR level', () => {
    expect(buildRulePayload({ ...base, assetType: 'gold', assetSymbol: 'ANTAM', ruleType: 'price_level', intent: 'buy_target', level: 1350000, currency: 'IDR' }))
      .toEqual({ asset_type: 'gold', asset_symbol: 'ANTAM', rule_type: 'price_level', intent: 'buy_target', level_idr: 1350000 })
  })

  it('validates the IDR range', () => {
    const idr = { ...base, ruleType: 'price_level', intent: 'buy_target', currency: 'IDR' }
    expect(validateRuleForm({ ...idr, level: 1.46e9 })).toBe('')
    expect(validateRuleForm({ ...idr, level: 0.00009 })).toBe('asset.rules.error_level_idr')
    expect(validateRuleForm({ ...idr, level: 1e12 })).toBe('asset.rules.error_level_idr')
  })

  it('maps an IDR rule into the form', () => {
    expect(ruleToForm({ asset_type: 'gold', asset_symbol: 'ANTAM', rule_type: 'price_level', intent: 'buy_target', direction: 'below',
      level_usd: null, level_currency: 'IDR', level_idr: 1350000, pct: null }))
      .toMatchObject({ currency: 'IDR', level: 1350000 })
  })

  it('formats the level in the rule currency', () => {
    expect(formatLevel({ level_currency: 'IDR', level_idr: 1350000 })).toBe(formatIDR(1350000))
    expect(formatLevel({ level_currency: 'USD', level_usd: 80 })).toBe(formatUSD(80))
    expect(formatLevel({ level_usd: 80 })).toBe(formatUSD(80)) // rules from before #40
  })

  it('summarises an IDR level in rupiah', () => {
    expect(ruleSummary({ rule_type: 'price_level', intent: 'buy_target', level_currency: 'IDR', level_idr: 1350000 }, t))
      .toBe(`asset.rules.summary_intent|${JSON.stringify({ intent: 'asset.rules.intent_buy_target', level: formatIDR(1350000) })}`)
  })
})

describe('changeAssetType', () => {
  const legacyAntam = { ...emptyRuleForm(), assetType: 'gold', assetSymbol: 'ANTAM', ruleType: 'price_level', intent: 'buy_target', level: 80, currency: 'USD' }

  it('re-picking the same type changes nothing (a legacy gold USD rule stays USD, Q7)', () => {
    expect(changeAssetType(legacyAntam, 'gold')).toEqual(legacyAntam)
  })

  it('a real change clears the symbol and applies the new default currency', () => {
    const form = { ...emptyRuleForm(), assetType: 'crypto', assetSymbol: 'BTC', ruleType: 'price_level', level: 90000, currency: 'USD' }
    expect(changeAssetType(form, 'gold')).toMatchObject({ assetType: 'gold', assetSymbol: null, currency: 'IDR', level: null })
  })

  it('keeps the level when the default currency does not change', () => {
    const form = { ...emptyRuleForm(), assetType: 'crypto', assetSymbol: 'BTC', ruleType: 'price_level', level: 90000, currency: 'USD' }
    expect(changeAssetType(form, 'stock')).toMatchObject({ assetType: 'stock', assetSymbol: null, currency: 'USD', level: 90000 })
  })

  it('drops week52_break when switching to gold', () => {
    const form = { ...emptyRuleForm(), assetType: 'crypto', ruleType: 'week52_break', direction: 'above' }
    expect(changeAssetType(form, 'gold').ruleType).toBeNull()
  })
})
