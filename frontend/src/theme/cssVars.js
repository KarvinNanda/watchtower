import { shared } from './tokens'

const kebab = (key) => key.replace(/[A-Z]/g, (c) => '-' + c.toLowerCase())

// toCssVars turns one theme's tokens (plus the shared scale) into --wt-*
// custom properties for our own CSS.
export function toCssVars(themeTokens) {
  const vars = {}
  for (const [key, value] of Object.entries(themeTokens)) vars[`--wt-${kebab(key)}`] = value
  for (const [key, value] of Object.entries(shared.space)) vars[`--wt-space-${key}`] = `${value}px`
  for (const [key, value] of Object.entries(shared.radius)) vars[`--wt-radius-${key}`] = `${value}px`
  vars['--wt-font-family'] = shared.fontFamily
  return vars
}

// applyCssVars writes vars onto a CSSStyleDeclaration (document.documentElement.style).
export function applyCssVars(style, vars) {
  for (const [name, value] of Object.entries(vars)) style.setProperty(name, value)
}
