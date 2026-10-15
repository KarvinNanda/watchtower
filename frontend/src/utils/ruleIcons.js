import { ArrowDown, ArrowUp, OctagonX, ShoppingCart, Sparkles, Target, TrendingDown, Zap } from '@lucide/vue'

// ruleIcon returns the @lucide/vue icon for a rule. Same meaning as the
// per-type emoji in Telegram messages (internal/alertmsg). Icons are imported
// by name: `import * as` would put every lucide icon in the bundle.
const INTENT_ICONS = { buy_target: ShoppingCart, take_profit: Target, stop_loss: OctagonX }

export function ruleIcon(rule) {
  switch (rule.rule_type) {
    case 'price_level':
      return INTENT_ICONS[rule.intent] ?? (rule.direction === 'below' ? ArrowDown : ArrowUp)
    case 'pct_change_24h':
      return Zap
    case 'trailing_stop':
      return TrendingDown
    default:
      return Sparkles
  }
}
