import { describe, it, expect } from 'vitest'
import * as lucide from '@lucide/vue'
import { ruleIcon } from './ruleIcons'

// Same meaning as the Telegram emoji (🛒 🎯 🛑 ⬆️ ⬇️ ⚡ 📉 🆕), as SVG icons.
describe('ruleIcon', () => {
  it.each([
    [{ rule_type: 'price_level', intent: 'buy_target' }, 'ShoppingCart'],
    [{ rule_type: 'price_level', intent: 'take_profit' }, 'Target'],
    [{ rule_type: 'price_level', intent: 'stop_loss' }, 'OctagonX'],
    [{ rule_type: 'price_level', direction: 'above' }, 'ArrowUp'],
    [{ rule_type: 'price_level', direction: 'below' }, 'ArrowDown'],
    [{ rule_type: 'pct_change_24h' }, 'Zap'],
    [{ rule_type: 'trailing_stop' }, 'TrendingDown'],
    [{ rule_type: 'week52_break', direction: 'above' }, 'Sparkles'],
  ])('%o → %s', (rule, name) => {
    expect(lucide[name], `${name} exists in @lucide/vue`).toBeTruthy()
    expect(ruleIcon(rule)).toBe(lucide[name])
  })
})
