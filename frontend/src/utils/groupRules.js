import { sortRulesBySymbol } from './assetRules'

// groupRulesBySymbol joins the rules with the market snapshot: one group per
// symbol, symbols sorted, rules by id. snapshot may be null when it failed to
// load; the rules still show (Spec 4a, section 6).
export function groupRulesBySymbol(rules, snapshot) {
  const markets = new Map((snapshot ?? []).map((m) => [m.symbol, m]))
  const groups = new Map()
  for (const rule of sortRulesBySymbol(rules)) {
    let group = groups.get(rule.asset_symbol)
    if (!group) {
      group = {
        symbol: rule.asset_symbol,
        assetType: rule.asset_type,
        market: markets.get(rule.asset_symbol) ?? null,
        rules: [],
      }
      groups.set(rule.asset_symbol, group)
    }
    group.rules.push(rule)
  }
  return [...groups.values()]
}
