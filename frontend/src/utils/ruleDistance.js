// How far the current price is from a rule's level (Spec 4a). Pure; all
// math in USD because levels are stored in USD.

// Mirrors intentDirections in backend/internal/user/asset_rules.go. Keep the
// two in sync; ruleDistance.test.js pins this table.
export const INTENT_SIDE = { buy_target: 'below', take_profit: 'above', stop_loss: 'below' }

// A level within this many percent of the price is shown as "near".
export const NEAR_PCT = 3

export function trailingStopLevel(rule) {
  if (rule.peak_price_usd == null) return null
  return rule.peak_price_usd * (1 - rule.pct / 100)
}

function levelDistance(level, side, price) {
  const pct = ((level - price) / price) * 100
  const passed = side === 'above' ? price >= level : price <= level
  return { kind: 'level', level, pct, passed, near: !passed && Math.abs(pct) <= NEAR_PCT }
}

export function ruleDistance(rule, market) {
  if (rule.supported === false) return null
  const price = market?.price_usd
  if (!(price > 0)) return null

  switch (rule.rule_type) {
    case 'price_level': {
      const side = rule.direction ?? INTENT_SIDE[rule.intent]
      if (!side || !(rule.level_usd > 0)) return null
      return levelDistance(rule.level_usd, side, price)
    }
    case 'trailing_stop': {
      const stop = trailingStopLevel(rule)
      return stop == null ? null : levelDistance(stop, 'below', price)
    }
    case 'pct_change_24h':
      return { kind: 'change24h', current: market.change_pct_24h ?? null, threshold: rule.pct }
    default:
      return null
  }
}
