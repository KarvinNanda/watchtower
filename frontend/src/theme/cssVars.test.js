import { describe, it, expect } from 'vitest'
import { tokens, shared } from './tokens'
import { toCssVars, applyCssVars } from './cssVars'

function fakeStyle() {
  const props = new Map()
  return { props, setProperty: (k, v) => props.set(k, v) }
}

describe('toCssVars', () => {
  it('emits one --wt-* variable per color token, kebab-cased', () => {
    const vars = toCssVars(tokens.dark)
    expect(vars['--wt-bg']).toBe(tokens.dark.bg)
    expect(vars['--wt-text-subtle']).toBe(tokens.dark.textSubtle)
    expect(vars['--wt-fired-tint']).toBe(tokens.dark.firedTint)
    for (const key of Object.keys(tokens.dark)) {
      const name = '--wt-' + key.replace(/[A-Z]/g, (c) => '-' + c.toLowerCase())
      expect(vars[name], name).toBe(tokens.dark[key])
    }
  })

  it('emits spacing and radius in px, and the font family', () => {
    const vars = toCssVars(tokens.dark)
    expect(vars['--wt-space-md']).toBe(`${shared.space.md}px`)
    expect(vars['--wt-radius-lg']).toBe(`${shared.radius.lg}px`)
    expect(vars['--wt-font-family']).toBe(shared.fontFamily)
  })
})

describe('applyCssVars', () => {
  it('switching dark to light overwrites every variable', () => {
    const style = fakeStyle()
    applyCssVars(style, toCssVars(tokens.dark))
    applyCssVars(style, toCssVars(tokens.light))
    for (const [name, value] of Object.entries(toCssVars(tokens.light))) {
      expect(style.props.get(name), name).toBe(value)
    }
  })
})
