// Pure helpers for the asset rule form and list. The server validates
// everything again; these only keep the form honest and send just the
// fields that apply to the chosen rule type (the API rejects the others).

export const RULE_TYPES = ['price_level', 'pct_change_24h', 'trailing_stop', 'week52_break']
export const INTENTS = ['none', 'buy_target', 'take_profit', 'stop_loss']

// Smallest percent the API accepts: pct is stored as DECIMAL(5,2).
export const MIN_PCT = 0.01

// IDR levels (#40): the backend stores level_idr DECIMAL(24,8).
export const MIN_LEVEL_IDR = 0.0001
export const MAX_LEVEL_IDR = 999999999999

// Gold (ANTAM) is quoted in rupiah at the source; everything else in USD.
export function defaultCurrency(assetType) {
  return assetType === 'gold' ? 'IDR' : 'USD'
}

// setCurrency switches the level currency. A typed level is cleared, never
// converted: the USD/IDR rate updates once a day (Spec 4b, Q6).
export function setCurrency(form, currency) {
  if (form.currency === currency) return form
  return { ...form, currency, level: null }
}

// changeAssetType applies a new asset type from the form's select. Picking
// the type that is already set changes nothing, so editing a legacy gold USD
// rule never flips it to IDR (Spec 4b, Q7). A real change clears the symbol,
// drops week52_break for gold and applies the type's default currency.
export function changeAssetType(form, assetType) {
  if (form.assetType === assetType) return form
  const ruleType = assetType === 'gold' && form.ruleType === 'week52_break' ? null : form.ruleType
  return setCurrency({ ...form, assetType, assetSymbol: null, ruleType }, defaultCurrency(assetType))
}

export function emptyRuleForm() {
  return { assetType: null, assetSymbol: null, ruleType: null, intent: 'none', direction: null, level: null, currency: 'USD', pct: null }
}

export function ruleToForm(rule) {
  return {
    assetType: rule.asset_type,
    assetSymbol: rule.asset_symbol,
    ruleType: rule.rule_type,
    intent: rule.intent ?? 'none',
    direction: rule.intent ? null : rule.direction,
    currency: rule.level_currency === 'IDR' ? 'IDR' : 'USD',
    level: rule.level_currency === 'IDR' ? rule.level_idr : rule.level_usd,
    pct: rule.pct,
  }
}

export function validateRuleForm(form) {
  switch (form.ruleType) {
    case 'price_level':
      if (form.currency === 'IDR') {
        if (!(form.level >= MIN_LEVEL_IDR && form.level <= MAX_LEVEL_IDR)) return 'asset.rules.error_level_idr'
      } else if (!(form.level > 0)) {
        return 'asset.rules.error_level'
      }
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
    case 'price_level': {
      const level = form.currency === 'IDR' ? { level_idr: form.level } : { level_usd: form.level }
      if (form.intent && form.intent !== 'none') return { ...base, intent: form.intent, ...level }
      return { ...base, direction: form.direction, ...level }
    }
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

// formatIDR keeps up to 4 decimals below Rp 1 so small crypto prices do
// not read as "Rp 0".
export function formatIDR(value) {
  if (value == null) return '—'
  const small = value !== 0 && Math.abs(value) < 1
  return new Intl.NumberFormat('id-ID', {
    style: 'currency',
    currency: 'IDR',
    minimumFractionDigits: 0,
    maximumFractionDigits: small ? 4 : 0,
  }).format(value)
}

const levelInputFormat = new Intl.NumberFormat('id-ID', { maximumFractionDigits: 4 })

// formatLevelInput shows an IDR level while it is typed: "1.460.000.000",
// so a missing zero is visible before saving (#60).
export function formatLevelInput(value) {
  return value == null ? '' : levelInputFormat.format(value)
}

// parseLevelInput reads id-ID input back: "." groups thousands in groups of
// exactly three, "," is the decimal mark, and "Rp" (the app's own format) is
// ignored. Empty input is no value. Anything else is NaN, so Naive keeps the
// previous value: a dot decimal like "2350000.50" would otherwise lose its
// dot and save the level 100 times too big. The value is rounded to the 4
// decimals the input shows.
const GROUPED_IDR = /^\d{1,3}(\.\d{3})+(,\d+)?$/
const PLAIN_IDR = /^\d+(,\d+)?$/

export function parseLevelInput(text) {
  const cleaned = String(text ?? '').replace(/rp/gi, '').replace(/[\s\u00a0]/g, '')
  if (cleaned === '') return null
  let normalized
  if (GROUPED_IDR.test(cleaned)) normalized = cleaned.replace(/\./g, '').replace(',', '.')
  else if (PLAIN_IDR.test(cleaned)) normalized = cleaned.replace(',', '.')
  else return NaN
  return Math.round(Number(normalized) * 1e4) / 1e4
}

export function formatLevel(rule) {
  return rule.level_currency === 'IDR' ? formatIDR(rule.level_idr) : formatUSD(rule.level_usd)
}

export function ruleSummary(rule, t) {
  const level = formatLevel(rule)
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
  // A symbol WatchTower cannot fetch never fires, whatever else is set.
  if (rule.supported === false) return 'unsupported'
  if (rule.rule_type === 'trailing_stop' && rule.triggered_at) return 'fired'
  return rule.is_active ? 'active' : 'paused'
}

export function sortRulesBySymbol(rules) {
  return [...rules].sort((a, b) => a.asset_symbol.localeCompare(b.asset_symbol) || a.id - b.id)
}

const pctFormat = new Intl.NumberFormat('en-US', {
  signDisplay: 'exceptZero',
  minimumFractionDigits: 1,
  maximumFractionDigits: 1,
})

// formatPct shows a signed percentage with one decimal ("+14.3%").
export function formatPct(value) {
  if (value == null) return '—'
  return `${pctFormat.format(value)}%`
}
