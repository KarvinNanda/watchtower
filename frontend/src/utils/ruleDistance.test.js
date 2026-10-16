import { describe, it, expect } from 'vitest'
import { ruleDistance, trailingStopLevel, INTENT_SIDE, NEAR_PCT } from './ruleDistance'

const market = (price, change = null) => ({ symbol: 'BTC', price_usd: price, change_pct_24h: change })
const level = (extra) => ({ id: 1, rule_type: 'price_level', supported: true, ...extra })

describe('INTENT_SIDE', () => {
  it('matches intentDirections in backend/internal/user/asset_rules.go', () => {
    expect(INTENT_SIDE).toEqual({ buy_target: 'below', take_profit: 'above', stop_loss: 'below' })
    expect(NEAR_PCT).toBe(3)
  })
})

describe('ruleDistance: price_level', () => {
  it('above, not reached', () => {
    const d = ruleDistance(level({ direction: 'above', level_usd: 110 }), market(100))
    expect(d).toEqual({ kind: 'level', level: 110, pct: 10, passed: false, near: false })
  })

  it('above, exactly at the level counts as passed', () => {
    expect(ruleDistance(level({ direction: 'above', level_usd: 100 }), market(100)).passed).toBe(true)
  })

  it('below via stop_loss intent, not reached', () => {
    const d = ruleDistance(level({ intent: 'stop_loss', level_usd: 95 }), market(100))
    expect(d.pct).toBeCloseTo(-5)
    expect(d.passed).toBe(false)
  })

  it('below via stop_loss intent, price under the level is passed', () => {
    expect(ruleDistance(level({ intent: 'stop_loss', level_usd: 95 }), market(94)).passed).toBe(true)
  })

  it('take_profit is above, buy_target is below', () => {
    expect(ruleDistance(level({ intent: 'take_profit', level_usd: 90 }), market(100)).passed).toBe(true)
    expect(ruleDistance(level({ intent: 'buy_target', level_usd: 90 }), market(100)).passed).toBe(false)
  })

  it('near when within NEAR_PCT and not passed', () => {
    expect(ruleDistance(level({ direction: 'above', level_usd: 103 }), market(100)).near).toBe(true)
    expect(ruleDistance(level({ direction: 'above', level_usd: 103.5 }), market(100)).near).toBe(false)
    expect(ruleDistance(level({ direction: 'above', level_usd: 99 }), market(100)).near).toBe(false) // passed
  })
})

describe('ruleDistance: trailing_stop', () => {
  const trailing = (peak) => ({ id: 2, rule_type: 'trailing_stop', pct: 10, peak_price_usd: peak, supported: true })

  it('stop is peak × (1 − pct/100)', () => {
    expect(trailingStopLevel(trailing(200))).toBeCloseTo(180)
    const d = ruleDistance(trailing(200), market(190))
    expect(d.level).toBeCloseTo(180)
    expect(d.pct).toBeCloseTo(-5.263, 2)
    expect(d.passed).toBe(false)
  })

  it('passed at or below the stop', () => {
    expect(ruleDistance(trailing(200), market(180)).passed).toBe(true)
  })

  it('no peak yet → null', () => {
    expect(trailingStopLevel(trailing(null))).toBeNull()
    expect(ruleDistance(trailing(null), market(190))).toBeNull()
  })
})

describe('ruleDistance: pct_change_24h', () => {
  const rule = { id: 3, rule_type: 'pct_change_24h', pct: 5, supported: true }

  it('reports current change against the threshold', () => {
    expect(ruleDistance(rule, market(100, -2.1))).toEqual({ kind: 'change24h', current: -2.1, threshold: 5 })
  })

  it('unknown current change is null', () => {
    expect(ruleDistance(rule, market(100, null)).current).toBeNull()
  })
})

describe('ruleDistance: no distance', () => {
  it.each([
    ['week52_break', { id: 4, rule_type: 'week52_break', direction: 'above', supported: true }, market(100)],
    ['market missing', level({ direction: 'above', level_usd: 110 }), undefined],
    ['price 0', level({ direction: 'above', level_usd: 110 }), market(0)],
    ['unsupported', level({ direction: 'above', level_usd: 110, supported: false }), market(100)],
  ])('%s → null', (_, rule, m) => {
    expect(ruleDistance(rule, m)).toBeNull()
  })
})

describe('ruleDistance: IDR price_level', () => {
  const antam = (extra) => ({ id: 9, rule_type: 'price_level', supported: true, level_currency: 'IDR', ...extra })

  it('compares level_idr with price_idr', () => {
    const d = ruleDistance(antam({ intent: 'buy_target', level_idr: 2350000 }), { price_usd: 130, price_idr: 2392157 })
    expect(d.level).toBe(2350000)
    expect(d.pct).toBeCloseTo(-1.762, 2)
    expect(d.passed).toBe(false)
    expect(d.near).toBe(true)
  })

  it('unknown price_idr → null', () => {
    expect(ruleDistance(antam({ intent: 'buy_target', level_idr: 2350000 }), { price_usd: 130, price_idr: null })).toBeNull()
  })
})

// #52: mirrors TestDistanceTo_Week52 in backend/internal/rules/distance_test.go.
describe('ruleDistance: week52_break', () => {
  const w52 = (extra) => ({ id: 4, rule_type: 'week52_break', supported: true, ...extra })

  it('measures against the stored 52-week reference', () => {
    expect(ruleDistance(w52({ direction: 'above', week52_ref_usd: 120 }), market(100)))
      .toEqual({ kind: 'level', level: 120, pct: 20, passed: false, near: false })
    expect(ruleDistance(w52({ direction: 'below', week52_ref_usd: 80 }), market(79)).passed).toBe(true)
    expect(ruleDistance(w52({ direction: 'below', week52_ref_usd: 80 }), market(82)).near).toBe(true)
  })

  it('not evaluated yet (no reference) → null', () => {
    expect(ruleDistance(w52({ direction: 'above', week52_ref_usd: null }), market(100))).toBeNull()
  })
})
