import { describe, it, expect } from 'vitest'
import { tokens } from './tokens'
import { naiveOverrides } from './naiveOverrides'

function luminance(hex) {
  const [r, g, b] = [1, 3, 5]
    .map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

// composite blends an rgba() tint over an opaque #rrggbb background.
function composite(rgba, hex) {
  const [r, g, b, alpha] = rgba.match(/[\d.]+/g).map(Number)
  const base = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16))
  const mixed = [r, g, b].map((c, i) => Math.round(c * alpha + base[i] * (1 - alpha)))
  return '#' + mixed.map((c) => c.toString(16).padStart(2, '0')).join('')
}

function contrast(a, b) {
  const [x, y] = [luminance(a), luminance(b)]
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05)
}

const TEXT_TOKENS = ['text', 'textMuted', 'textSubtle', 'primary', 'up', 'down', 'fired', 'armed', 'stale', 'danger']

describe('tokens', () => {
  it('dark and light define the same keys', () => {
    expect(Object.keys(tokens.light).sort()).toEqual(Object.keys(tokens.dark).sort())
  })

  it.each(['dark', 'light'])('%s colors are #rrggbb, tints are rgba()', (name) => {
    for (const [key, value] of Object.entries(tokens[name])) {
      if (key.endsWith('Tint')) expect(value, key).toMatch(/^rgba\(\d+, \d+, \d+, 0?\.\d+\)$/)
      else expect(value, key).toMatch(/^#[0-9a-f]{6}$/)
    }
  })

  it.each(['dark', 'light'])('%s text tokens reach 4.5:1 on surface and bg', (name) => {
    const t = tokens[name]
    for (const key of TEXT_TOKENS) {
      expect(contrast(t[key], t.surface), `${key} on surface`).toBeGreaterThanOrEqual(4.5)
      expect(contrast(t[key], t.bg), `${key} on bg`).toBeGreaterThanOrEqual(4.5)
    }
  })

  // Text is also drawn on the card header (surfaceRaised) and on a row
  // highlighted with firedTint over surface.
  it.each(['dark', 'light'])('%s text tokens reach 4.5:1 on surfaceRaised and the highlighted row', (name) => {
    const t = tokens[name]
    const highlighted = composite(t.firedTint, t.surface)
    for (const key of TEXT_TOKENS) {
      expect(contrast(t[key], t.surfaceRaised), `${key} on surfaceRaised`).toBeGreaterThanOrEqual(4.5)
      expect(contrast(t[key], highlighted), `${key} on highlighted row`).toBeGreaterThanOrEqual(4.5)
    }
  })

  it.each(['dark', 'light'])('%s button text reaches 4.5:1 on primary', (name) => {
    expect(contrast(tokens[name].onPrimary, tokens[name].primary)).toBeGreaterThanOrEqual(4.5)
  })
})

describe('naiveOverrides', () => {
  it.each(['dark', 'light'])('%s maps tokens onto Naive common vars', (name) => {
    const t = tokens[name]
    const { common } = naiveOverrides(t)
    expect(common.bodyColor).toBe(t.bg)
    expect(common.baseColor).toBe(t.onPrimary)
    expect(common.cardColor).toBe(t.surface)
    expect(common.borderColor).toBe(t.border)
    expect(common.primaryColor).toBe(t.primary)
    expect(common.successColor).toBe(t.up)
    expect(common.warningColor).toBe(t.fired)
    expect(common.errorColor).toBe(t.danger)
    expect(common.textColor3).toBe(t.textSubtle)
  })

  it('uses no value that is not a token', () => {
    const values = new Set(Object.values(tokens.dark))
    for (const [key, value] of Object.entries(naiveOverrides(tokens.dark).common)) {
      expect(values.has(value), key).toBe(true)
    }
  })
})
