// Pure helpers for the asset rule form and list. The server validates
// everything again; these only keep the form honest and send just the
// fields that apply to the chosen rule type (the API rejects the others).

export const RULE_TYPES = ['price_level', 'pct_change_24h', 'trailing_stop', 'week52_break']
export const INTENTS = ['none', 'buy_target', 'take_profit', 'stop_loss']

// Smallest percent the API accepts: pct is stored as DECIMAL(5,2).
export const MIN_PCT = 0.01

export function emptyRuleForm() {
  return { assetType: null, assetSymbol: null, ruleType: null, intent: 'none', direction: null, levelUsd: null, pct: null }
}

export function ruleToForm(rule) {
  return {
    assetType: rule.asset_type,
    assetSymbol: rule.asset_symbol,
    ruleType: rule.rule_type,
    intent: rule.intent ?? 'none',
    direction: rule.intent ? null : rule.direction,
    levelUsd: rule.level_usd,
    pct: rule.pct,
  }
}

export function validateRuleForm(form) {
  switch (form.ruleType) {
    case 'price_level':
      if (!(form.levelUsd > 0)) return 'asset.rules.error_level'
      if (form.intent === 'none' && !form.direction) return 'asset.rules.error_direction'
      return ''
    case 'pct_change_24h':
      return form.pct >= MIN_PCT && form.pct <= 100 ? '' : 'asset.rules.error_pct'
    case 'trailing_stop':
      return form.pct >= MIN_PCT && form.pct < 100 ? '' : 'asset.rules.error_pct_trailing'
    case 'week52_break':
      return form.direction ? '' : 'asset.rules.error_direction'
    default:
      return 'common.required_field'
  }
}

export function buildRulePayload(form) {
  const base = { asset_type: form.assetType, asset_symbol: form.assetSymbol, rule_type: form.ruleType }
  switch (form.ruleType) {
    case 'price_level':
      if (form.intent && form.intent !== 'none') return { ...base, intent: form.intent, level_usd: form.levelUsd }
      return { ...base, direction: form.direction, level_usd: form.levelUsd }
    case 'pct_change_24h':
    case 'trailing_stop':
      return { ...base, pct: form.pct }
    case 'week52_break':
      return { ...base, direction: form.direction }
    default:
      return base
  }
}

export function formatUSD(value) {
  if (value == null) return '—'
  const small = value !== 0 && Math.abs(value) < 1
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: small ? 0 : 2,
    maximumFractionDigits: small ? 8 : 2,
  }).format(value)
}

export function ruleSummary(rule, t) {
  const level = formatUSD(rule.level_usd)
  switch (rule.rule_type) {
    case 'price_level':
      if (rule.intent) return t('asset.rules.summary_intent', { intent: t(`asset.rules.intent_${rule.intent}`), level })
      return t(`asset.rules.summary_level_${rule.direction}`, { level })
    case 'pct_change_24h':
      return t('asset.rules.summary_pct', { pct: rule.pct })
    case 'trailing_stop':
      return t('asset.rules.summary_trailing', { pct: rule.pct })
    case 'week52_break':
      return t(`asset.rules.week52_${rule.direction}`)
    default:
      return rule.rule_type
  }
}

export function ruleState(rule) {
  if (rule.rule_type === 'trailing_stop' && rule.triggered_at) return 'fired'
  return rule.is_active ? 'active' : 'paused'
}

export function sortRulesBySymbol(rules) {
  return [...rules].sort((a, b) => a.asset_symbol.localeCompare(b.asset_symbol) || a.id - b.id)
}
